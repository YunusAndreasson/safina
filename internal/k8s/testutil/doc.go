// Package testutil collects shared scaffolding for safina's k8s tests:
// fake clientset builders, recorded JSON fixture loaders, and small
// typed object factories. It is a normal (non-_test) subpackage so any
// other test package can import it — mirroring k9s's internal/config/mock
// pattern.
//
// Fixtures live in ./testdata/*.json. Add a fixture by saving the output
// of `kubectl get <resource> <name> -o json` into the directory; load
// with LoadPod / LoadNode / LoadUnstructured.
package testutil
