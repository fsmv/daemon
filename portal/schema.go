package portal

import _ "embed"

// The text of the service.proto file so that clients can print the schema.
//go:embed service.proto
var ServiceProto string
