// Package helperabovetype keeps a helper above a type although a constructor
// below the type calls it: only moving the type could put the helper after it.
package helperabovetype

func defaultName() string { return "default" }

// Config configures.
type Config struct{ name string }

// NewConfig returns a config.
func NewConfig() *Config { return &Config{name: defaultName()} }
