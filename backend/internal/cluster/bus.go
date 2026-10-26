package cluster

import "sync"

// Bus moves Envelopes between nodes. Delivery is asynchronous and, between
// one sender and one receiver, in send order — a game_start sent after a
// "seated" notice arrives after it. A node never needs to Send to itself
// (the hub short-cuts local work), and Broadcast does not echo back.
type Bus interface {
	// Send queues e for node; it never blocks on the network.
	Send(node string, e Envelope)
	// Broadcast queues e for every other node.
	Broadcast(e Envelope)
	// Start begins delivering inbound envelopes to handle, one at a time.
	Start(handle func(Envelope))
	Close()
}

// MemNetwork connects in-process buses: one per node, as many nodes as a
// test wants, or exactly one for a single-instance server.
type MemNetwork struct {
	mu    sync.Mutex
	nodes map[string]*MemBus
}

// NewMemNetwork returns an empty in-process network.
func NewMemNetwork() *MemNetwork {
	return &MemNetwork{nodes: make(map[string]*MemBus)}
}

// Join attaches node to the network and returns its bus.
func (n *MemNetwork) Join(node string) *MemBus {
	b := &MemBus{net: n, node: node, box: newMailbox()}
	n.mu.Lock()
	n.nodes[node] = b
	n.mu.Unlock()
	return b
}

// Leave detaches node: later envelopes for it are dropped, like a dead
// process's pub/sub channel.
func (n *MemNetwork) Leave(node string) {
	n.mu.Lock()
	b := n.nodes[node]
	delete(n.nodes, node)
	n.mu.Unlock()
	if b != nil {
		b.box.close()
	}
}

func (n *MemNetwork) peer(node string) *MemBus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.nodes[node]
}

func (n *MemNetwork) others(self string) []*MemBus {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]*MemBus, 0, len(n.nodes))
	for id, b := range n.nodes {
		if id != self {
			out = append(out, b)
		}
	}
	return out
}

// MemBus is one node's end of a MemNetwork.
type MemBus struct {
	net  *MemNetwork
	node string
	box  *mailbox
}

func (b *MemBus) Send(node string, e Envelope) {
	e.From = b.node
	if p := b.net.peer(node); p != nil {
		p.box.put(e)
	}
}

func (b *MemBus) Broadcast(e Envelope) {
	e.From = b.node
	for _, p := range b.net.others(b.node) {
		p.box.put(e)
	}
}

func (b *MemBus) Start(handle func(Envelope)) { go b.box.drain(handle) }

func (b *MemBus) Close() { b.net.Leave(b.node) }

// mailbox is an unbounded FIFO: put never blocks (a slow node must not
// stall the sender's game actor), drain hands items over one at a time.
type mailbox struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []Envelope
	closed bool
}

func newMailbox() *mailbox {
	m := &mailbox{}
	m.cond = sync.NewCond(&m.mu)
	return m
}

func (m *mailbox) put(e Envelope) {
	m.mu.Lock()
	if !m.closed {
		m.items = append(m.items, e)
		m.cond.Signal()
	}
	m.mu.Unlock()
}

func (m *mailbox) close() {
	m.mu.Lock()
	m.closed = true
	m.items = nil
	m.cond.Broadcast()
	m.mu.Unlock()
}

func (m *mailbox) drain(handle func(Envelope)) {
	for {
		m.mu.Lock()
		for len(m.items) == 0 && !m.closed {
			m.cond.Wait()
		}
		if m.closed {
			m.mu.Unlock()
			return
		}
		e := m.items[0]
		m.items = m.items[1:]
		m.mu.Unlock()
		handle(e)
	}
}
