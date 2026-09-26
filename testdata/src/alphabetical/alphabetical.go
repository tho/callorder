// Package alphabetical sorts constructors and methods by name, like funcorder.
package alphabetical

// Greeter greets.
type Greeter struct{ name string }

// NewGreeter returns a greeter.
func NewGreeter() *Greeter { return &Greeter{name: "you"} } // want `expected MustGreeter before NewGreeter: constructors and methods are sorted by name \(funcorder alphabetical\)`

// MustGreeter returns a greeter.
func MustGreeter() *Greeter { return NewGreeter() }

// Morning greets in the morning.
func (g *Greeter) Morning() string { return g.polite("good morning") }

// Evening greets in the evening.
func (g *Greeter) Evening() string { return g.polite("good evening") }

func (g *Greeter) polite(greeting string) string { return greeting + ", " + g.address() }

// address sorts before polite although polite calls it.
func (g *Greeter) address() string { return "dear " + g.name }
