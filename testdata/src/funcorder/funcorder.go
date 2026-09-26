// Package funcorder follows funcorder's default constructor and struct-method checks.
package funcorder

// Store holds items.
type Store struct{ items []string }

func (s *Store) add(item string) { s.items = append(s.items, item) } // want `expected NewStore before add: constructors come before their type's methods \(funcorder constructor\)`

// NewStore returns a store holding items.
func NewStore(items ...string) *Store {
	s := &Store{}
	for _, item := range items {
		s.add(item)
	}

	return s
}

// Len counts the items.
func (s *Store) Len() int { return len(s.items) }

// Add adds an item and logs it.
func (s *Store) Add(item string) {
	s.add(item)
	s.log()
}

func (s *Store) log() {}
