package portal

import _ "embed"

// The text of the service.proto file so that clients can print the schema.
//
//go:embed service.proto
var ServiceProto string

//go:generate protoc service.proto --go_out ./ --go-grpc_out ./ --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative

// Run the sub-package protoc as well just so it's easy to generate everything
//go:generate protoc portal/storage.proto -I ./ --go_out ./ --go_opt=paths=source_relative
