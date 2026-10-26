package cluster

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisBus carries envelopes over Redis pub/sub: one channel per node plus
// one for broadcasts. Outbound envelopes go through a single queue and a
// single publisher goroutine, which keeps them in send order (pub/sub then
// delivers each channel in order) and keeps network waits off the caller —
// often a game actor. The publisher sends whatever has queued up as one
// pipeline, so throughput is not capped at one message per round trip.
type RedisBus struct {
	rdb  *redis.Client
	node string
	out  chan outbound
	stop chan struct{}
	done chan struct{}
	sub  *redis.PubSub
	once sync.Once
}

type outbound struct {
	channel string
	data    []byte
}

const (
	busPrefix    = "gomoku:node:"
	busAll       = "gomoku:all"
	busQueue     = 1 << 14
	busFullWait  = 50 * time.Millisecond
	busPublishTO = time.Second
	busBatch     = 256 // most envelopes per pipeline
)

// NewRedisBus prepares node's bus; nothing flows until Start.
func NewRedisBus(rdb *redis.Client, node string) *RedisBus {
	return &RedisBus{
		rdb:  rdb,
		node: node,
		out:  make(chan outbound, busQueue),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

func (b *RedisBus) Send(node string, e Envelope) { b.enqueue(busPrefix+node, e) }

func (b *RedisBus) Broadcast(e Envelope) { b.enqueue(busAll, e) }

func (b *RedisBus) enqueue(channel string, e Envelope) {
	e.From = b.node
	data, err := json.Marshal(e)
	if err != nil {
		slog.Warn("cluster: encode envelope", "err", err)
		return
	}
	select {
	case b.out <- outbound{channel, data}:
		return
	default:
	}
	// Backed up (Redis slow or down): wait briefly, then drop rather than
	// stall the caller. Lost frames heal on the next full-state snapshot.
	t := time.NewTimer(busFullWait)
	defer t.Stop()
	select {
	case b.out <- outbound{channel, data}:
	case <-t.C:
		slog.Warn("cluster: bus queue full, dropping", "kind", e.Kind)
	case <-b.stop:
	}
}

// Start subscribes (returning once the subscription is live, so nothing
// sent to this node afterwards is missed) and starts the publisher.
func (b *RedisBus) Start(handle func(Envelope)) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b.sub = b.rdb.Subscribe(ctx, busPrefix+b.node, busAll)
	if _, err := b.sub.Receive(ctx); err != nil {
		slog.Error("cluster: subscribe", "err", err)
	}
	go b.publish()
	go func() {
		for msg := range b.sub.Channel() {
			var e Envelope
			if err := json.Unmarshal([]byte(msg.Payload), &e); err != nil {
				slog.Warn("cluster: decode envelope", "err", err)
				continue
			}
			if e.From == b.node {
				continue // our own broadcast
			}
			handle(e)
		}
	}()
}

func (b *RedisBus) publish() {
	defer close(b.done)
	batch := make([]outbound, 0, busBatch)
	for {
		select {
		case <-b.stop:
			return
		case o := <-b.out:
			batch = append(batch[:0], o)
		drain:
			for len(batch) < busBatch {
				select {
				case o := <-b.out:
					batch = append(batch, o)
				default:
					break drain
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), busPublishTO)
			pipe := b.rdb.Pipeline()
			for _, o := range batch {
				pipe.Publish(ctx, o.channel, o.data)
			}
			if _, err := pipe.Exec(ctx); err != nil {
				slog.Warn("cluster: publish", "batch", len(batch), "err", err)
			}
			cancel()
		}
	}
}

// Close flushes what is queued (bounded), then unsubscribes. Idempotent.
func (b *RedisBus) Close() {
	b.once.Do(func() {
		deadline := time.Now().Add(2 * time.Second)
		for len(b.out) > 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		close(b.stop)
		if b.sub != nil {
			<-b.done
			_ = b.sub.Close()
		}
	})
}
