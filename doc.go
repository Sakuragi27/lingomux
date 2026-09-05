// Package lingomux is an embeddable translation router, not a standalone
// service. A Client translates one text segment per call through an explicitly
// selected Provider or serial automatic fallback in provider registration
// order. Provider implementations can live in any package that imports
// lingomux; the root package does not depend on the built-in provider adapters.
package lingomux
