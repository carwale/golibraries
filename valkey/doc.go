// Package valkey is a Valkey client for Go services, built on github.com/valkey-io/valkey-go. It is
// the Go counterpart of the C# AEPLCore.Valkey library.
//
// NewClient connects once and hands out three views of the same connections:
//
//   - Client.Cache (package cache): cache-aside semantics. Errors are logged and swallowed, misses
//     fall back to the loader, and the critical-read sentinel ("_valkey_critical_") is shared with
//     the C# library.
//   - Client.Store (package store): Valkey as the system of record for ephemeral state. Every error
//     is returned.
//   - Client.Publisher and Client.NewSubscriber (package pubsub): classic, sharded and pattern
//     subscriptions with automatic resubscription and lifecycle callbacks.
//
// Package layout:
//
//	valkey/              Client, Config, Option (this package)
//	valkey/cache         ICache, NoOp, typed cache-aside helpers
//	valkey/store         IStore, Script, typed helpers
//	valkey/pubsub        IPublisher, ISubscriber, subscription options
//	valkey/transcoder    ITranscoder and the default JSON transcoder
//	valkey/internal/...  IAM token signing and shared helpers
//
// Authentication defaults to ElastiCache RBAC/IAM (TLS required, tokens generated and refreshed by
// the library); password and no-auth modes exist for local development and tests.
package valkey
