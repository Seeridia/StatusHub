// Package api embeds the public HTTP contract in the server binary.
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte
