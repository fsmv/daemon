package gate

import _ "embed"

// The text of the service.proto file so that clients can print the schema.
//
//go:embed service.proto
var ServiceProto string

//go:generate protoc -I ../ ../gate/service.proto --go_out ../ --go-grpc_out ../ --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative
//go:generate protoc -I ../ ../embedportal/storage.proto --go_out ../ --go_opt=paths=source_relative
