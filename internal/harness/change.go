package harness

// Target identifies the kind of configuration a Change updates.
type Target string

const (
	// EnvShell is rendered into the supported shell-specific environment files.
	EnvShell Target = "env_shell"
	// EnvFile is a KEY=VALUE environment file.
	EnvFile Target = "env_file"
	// JSONKey is a dot-separated key in a JSON file.
	JSONKey Target = "json_key"
)

// Change is one documented environment or configuration write, expressed as data.
// The setup context applies changes; adapters never write them directly.
type Change struct {
	Target Target
	// OS limits a change to the named operating systems. Empty means all.
	OS []string

	Path  string
	Key   string
	Value string

	// MergeList appends Value to an existing comma-separated list.
	MergeList bool
	// Exclusive makes an existing different value a setup conflict.
	Exclusive bool
	// HeadersKey and ProtocolKey locate values used when adopting an existing export.
	HeadersKey  string
	ProtocolKey string
	// Signal is empty for all signals, or traces, metrics, or logs.
	Signal string
}
