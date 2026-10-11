# KNOWN_ISSUES — 已知问题（如实记录，不隐瞒）

| # | 现象 | 已尝试 | 推测原因 | 状态 |
|---|---|---|---|---|
| K1 | 预置 `GITHUB_BURNER_PAT` 调用 GitHub API 返回 401 Bad credentials | 用 `gh`（devin-ai-integration app）尝试建仓 → `createRepository` 不被授权；App 能列仓不能建仓 | PAT 已吊销/过期 | ✅ 已由用户提供新 PAT 解决（建仓成功） |
