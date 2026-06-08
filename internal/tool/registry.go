package tool

import "sort"

// Registry holds the available tools. Built-ins are kept as a sorted, stable
// prefix; any future MCP tools would be appended and sorted separately. The doc
// notes this ordering matters for prompt-cache stability of the tool schema.
type Registry struct {
	byName map[string]Tool
	order  []string
}

// NewRegistry builds a registry from a set of tools, de-duplicated by name and
// sorted for stable ordering.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{byName: map[string]Tool{}}
	for _, t := range tools {
		if _, exists := r.byName[t.Name()]; exists {
			continue
		}
		r.byName[t.Name()] = t
		r.order = append(r.order, t.Name())
	}
	sort.Strings(r.order)
	return r
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// All returns every tool in stable order.
func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	return out
}

// Names returns the tool names in stable order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}
