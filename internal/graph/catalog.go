package graph

// propCatalog is the in-memory property-key intern table. Phase 5 replaces it
// with the persistent catalog; ids assigned here are what records store.
type propCatalog struct {
	byName map[string]uint32
	names  []string // index is the id; names[0] is unused
	next   uint32
}

func newPropCatalog() *propCatalog {
	return &propCatalog{
		byName: map[string]uint32{},
		names:  []string{""},
		next:   1,
	}
}

func catalogFrom(keys []string) *propCatalog {
	c := newPropCatalog()
	if len(keys) == 0 {
		return c
	}
	c.names = append([]string(nil), keys...)
	if len(c.names) == 0 {
		c.names = []string{""}
	}
	if c.names[0] != "" {
		c.names = append([]string{""}, c.names...)
	}
	c.byName = make(map[string]uint32, len(c.names))
	for id, name := range c.names {
		if id == 0 || name == "" {
			continue
		}
		c.byName[name] = uint32(id)
	}
	c.next = uint32(len(c.names))
	return c
}

func (c *propCatalog) intern(name string) uint32 {
	if id, ok := c.byName[name]; ok {
		return id
	}
	id := c.next
	c.next++
	c.byName[name] = id
	if int(id) != len(c.names) {
		// ids are dense; a gap means the table was built from a snapshot.
		for uint32(len(c.names)) < id {
			c.names = append(c.names, "")
		}
	}
	c.names = append(c.names, name)
	return id
}

func (c *propCatalog) lookup(name string) (uint32, bool) {
	id, ok := c.byName[name]
	return id, ok
}

func (c *propCatalog) has(id uint32) bool {
	return id > 0 && int(id) < len(c.names) && c.names[id] != ""
}

func (c *propCatalog) name(id uint32) (string, bool) {
	if !c.has(id) {
		return "", false
	}
	return c.names[id], true
}

func (c *propCatalog) export() []string {
	out := make([]string, len(c.names))
	copy(out, c.names)
	return out
}
