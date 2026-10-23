package main

// version is set by the release build (-ldflags "-X main.version=2.0.3").
var version = "dev"

const (
	// apiVersion is the API this bridge offers. Bump it when clients need something new from the bridge;
	// a client that needs more than the bridge offers tells its admin to update the bridge.
	//   1: /v1/chat, /v1/run, /health
	//   2: /v1/chat also returns token usage
	apiVersion = 2
	// apiMin is the oldest API clients may still speak. Raise it only for breaking changes; a client
	// speaking less is told to update.
	apiMin = 1
)

// features lists what this bridge can do, for clients that check single capabilities.
var features = []string{"chat", "chat.usage", "run", "web"}
