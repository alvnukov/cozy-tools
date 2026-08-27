package tool

import (
	"fmt"
	"slices"
)

// Catalog is an immutable, ordered collection of tools.
type Catalog struct {
	tools []Tool
	index map[string]int
}

// NewCatalog validates tools and rejects duplicate names.
func NewCatalog(tools ...Tool) (*Catalog, error) {
	catalog := &Catalog{
		tools: make([]Tool, 0, len(tools)),
		index: make(map[string]int, len(tools)),
	}
	for _, candidate := range tools {
		if err := candidate.Validate(); err != nil {
			return nil, fmt.Errorf("tool %q: %w", candidate.Spec.Name, err)
		}
		if _, exists := catalog.index[candidate.Spec.Name]; exists {
			return nil, fmt.Errorf("duplicate tool %q", candidate.Spec.Name)
		}
		candidate.Spec = candidate.Spec.clone()
		catalog.index[candidate.Spec.Name] = len(catalog.tools)
		catalog.tools = append(catalog.tools, candidate)
	}
	return catalog, nil
}

// MustCatalog is NewCatalog for static composition.
func MustCatalog(tools ...Tool) *Catalog {
	catalog, err := NewCatalog(tools...)
	if err != nil {
		panic(err)
	}
	return catalog
}

// Merge combines catalogs in order and rejects duplicate names.
func Merge(catalogs ...*Catalog) (*Catalog, error) {
	var tools []Tool
	for _, catalog := range catalogs {
		if catalog != nil {
			tools = append(tools, catalog.Tools()...)
		}
	}
	return NewCatalog(tools...)
}

// Lookup returns a defensive copy of the named tool.
func (c *Catalog) Lookup(name string) (Tool, bool) {
	if c == nil {
		return Tool{}, false
	}
	position, ok := c.index[name]
	if !ok {
		return Tool{}, false
	}
	tool := c.tools[position]
	tool.Spec = tool.Spec.clone()
	return tool, true
}

// Tools returns tools in declaration order.
func (c *Catalog) Tools() []Tool {
	if c == nil {
		return nil
	}
	out := slices.Clone(c.tools)
	for i := range out {
		out[i].Spec = out[i].Spec.clone()
	}
	return out
}

// Names returns tool names in declaration order.
func (c *Catalog) Names() []string {
	if c == nil {
		return nil
	}
	names := make([]string, len(c.tools))
	for i, candidate := range c.tools {
		names[i] = candidate.Spec.Name
	}
	return names
}

// Len returns the number of tools.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.tools)
}
