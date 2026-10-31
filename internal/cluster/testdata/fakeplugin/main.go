// Command fakeplugin is a kubeconfig exec credential plugin used in tests.
// FAKEPLUGIN_MODE=ok prints a token; FAKEPLUGIN_MODE=fail writes to stderr and exits 1.
package main

import (
	"fmt"
	"os"
)

func main() {
	switch os.Getenv("FAKEPLUGIN_MODE") {
	case "fail":
		fmt.Fprintln(os.Stderr, "error: SSO session expired; run `fake login` (token=leaky-secret)")
		os.Exit(1)
	default:
		fmt.Printf(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":%q}}`, os.Getenv("FAKEPLUGIN_TOKEN"))
	}
}
