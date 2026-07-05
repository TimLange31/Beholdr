module github.com/delangetimm/beholdr

go 1.22

require (
	k8s.io/api v0.30.2
	k8s.io/apimachinery v0.30.2
	k8s.io/client-go v0.30.2
	k8s.io/metrics v0.30.2
)

// Run `go mod tidy` once (with network access) to resolve indirect
// dependencies and generate go.sum.
