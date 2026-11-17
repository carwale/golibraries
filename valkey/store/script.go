package store

import valkeygo "github.com/valkey-io/valkey-go"

// Script is a Lua script run with EVALSHA, falling back to EVAL when the script is not loaded.
// Create it once (for example as a package variable) and reuse it.
type Script struct {
	lua *valkeygo.Lua
}

// NewScript wraps Lua source. In cluster mode every key a script touches must hash to the same
// slot; use a {hash tag} in the key names.
func NewScript(src string) *Script {
	return &Script{lua: valkeygo.NewLuaScript(src)}
}
