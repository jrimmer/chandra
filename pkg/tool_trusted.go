package pkg

// TrustedTool is the interface for Tier 2 (trusted, in-process) tools.
// Tier 2 tools declare their capabilities explicitly; the runtime validates
// them before dispatching. Unlike Tier 1 (built-in), Tier 2 tools are
// external Go packages compiled in with declared capability restrictions.
type TrustedTool interface {
	Tool                                // inherits Definition() and Execute()
	DeclaredCapabilities() []Capability // explicit capability declaration
}
