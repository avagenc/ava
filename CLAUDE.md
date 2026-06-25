# ava

Avagenc orchestrator agent module.

## Design decisions

**`Config.Tools` does not exist.** Tools are defined by the module itself, not injected by the consumer. When a new capability is added (e.g. Spotify), it is wired internally here, not passed in via config. `Config` only accepts `SubAgents` — specialists the consumer owns and manages.
