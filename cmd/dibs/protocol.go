package main

import (
	_ "embed"
	"fmt"
)

//go:embed agent-protocol-v1.md
var protocolDoc string

func runProtocol() {
	fmt.Print(protocolDoc)
}
