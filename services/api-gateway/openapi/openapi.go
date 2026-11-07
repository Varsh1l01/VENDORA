// Package openapi embeds the gateway's OpenAPI document and a Swagger UI page.
package openapi

import _ "embed"

// Spec is the OpenAPI 3.1 document for /api/v1.
//
//go:embed openapi.yaml
var Spec []byte

// SwaggerUI is a static page rendering Spec.
//
//go:embed swagger.html
var SwaggerUI []byte
