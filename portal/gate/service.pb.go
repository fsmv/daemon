// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        v3.21.12
// source: gate/service.proto

package gate

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Hostname struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Hostname string `protobuf:"bytes,1,opt,name=hostname,proto3" json:"hostname,omitempty"`
}

func (x *Hostname) Reset() {
	*x = Hostname{}
	if protoimpl.UnsafeEnabled {
		mi := &file_gate_service_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Hostname) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Hostname) ProtoMessage() {}

func (x *Hostname) ProtoReflect() protoreflect.Message {
	mi := &file_gate_service_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Hostname.ProtoReflect.Descriptor instead.
func (*Hostname) Descriptor() ([]byte, []int) {
	return file_gate_service_proto_rawDescGZIP(), []int{0}
}

func (x *Hostname) GetHostname() string {
	if x != nil {
		return x.Hostname
	}
	return ""
}

type RegisterRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// For HTTP: A url pattern that works with http.DefaultServMux. Ex: /images/
	// For TCP: ":tcp:port" for the port number portal should listen on. Only tcp
	// is accepted for now.
	//
	// HTTP patterns optionally accept a hostname (URL) constraint prefix. Or if
	// portal is configured to use the default hostname for no hostname patterns,
	// you can use * for the hostname to always match all URLs. For example:
	//
	//	ask.systems/images/
	//	*/favicon.ico
	Pattern string `protobuf:"bytes,1,opt,name=pattern,proto3" json:"pattern,omitempty"` // TODO: maybe support multiple patterns for the same IP/port
	// Set for third party web interfaces (or TCP proxy backends) that can't
	// use an random lease port.
	// Must be outside the range of portal's automatic ports.
	FixedPort uint32 `protobuf:"varint,2,opt,name=fixed_port,json=fixedPort,proto3" json:"fixed_port,omitempty"`
	// Optional: If set, forward the requests for pattern to this IP/hostname.
	// If unset, forward requests to the IP that sent the RegisterRequest.
	//
	// It is easiest to just run assimilate on the machine you want the forwarding
	// rule for, but if you have a cpanel-only host or otherwise don't have access
	// to run arbitrary code then you can use this setting to run assimilate on
	// another machine.
	Hostname string `protobuf:"bytes,6,opt,name=hostname,proto3" json:"hostname,omitempty"`
	// If true, remove the pattern in the URL of HTTP requests we forward to the
	// backend to hide that it is behind a reverse proxy.
	//
	// Ignored for TCP proxies.
	StripPattern bool `protobuf:"varint,3,opt,name=strip_pattern,json=stripPattern,proto3" json:"strip_pattern,omitempty"`
	// If true, do not redirect HTTP requests to HTTPS. This means the data will
	// be sent in plain-text readable to anyone if the client requests it. Some
	// legacy systems require plain HTTP requests. Leave this off by default for
	// security, that way responses will only be readable by the client.
	//
	// Ignored for TCP proxies.
	AllowHttp bool `protobuf:"varint,5,opt,name=allow_http,json=allowHttp,proto3" json:"allow_http,omitempty"`
	// If set, the server will sign the certificate request with portal's
	// certificate as the root and accept connections to the signed cert. This way
	// network traffic behind the reverse proxy can be encrypted.
	CertificateRequest []byte `protobuf:"bytes,4,opt,name=certificate_request,json=certificateRequest,proto3" json:"certificate_request,omitempty"`
}

func (x *RegisterRequest) Reset() {
	*x = RegisterRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_gate_service_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RegisterRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RegisterRequest) ProtoMessage() {}

func (x *RegisterRequest) ProtoReflect() protoreflect.Message {
	mi := &file_gate_service_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RegisterRequest.ProtoReflect.Descriptor instead.
func (*RegisterRequest) Descriptor() ([]byte, []int) {
	return file_gate_service_proto_rawDescGZIP(), []int{1}
}

func (x *RegisterRequest) GetPattern() string {
	if x != nil {
		return x.Pattern
	}
	return ""
}

func (x *RegisterRequest) GetFixedPort() uint32 {
	if x != nil {
		return x.FixedPort
	}
	return 0
}

func (x *RegisterRequest) GetHostname() string {
	if x != nil {
		return x.Hostname
	}
	return ""
}

func (x *RegisterRequest) GetStripPattern() bool {
	if x != nil {
		return x.StripPattern
	}
	return false
}

func (x *RegisterRequest) GetAllowHttp() bool {
	if x != nil {
		return x.AllowHttp
	}
	return false
}

func (x *RegisterRequest) GetCertificateRequest() []byte {
	if x != nil {
		return x.CertificateRequest
	}
	return nil
}

type Lease struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Pattern string                 `protobuf:"bytes,1,opt,name=pattern,proto3" json:"pattern,omitempty"`
	Address string                 `protobuf:"bytes,5,opt,name=address,proto3" json:"address,omitempty"`
	Port    uint32                 `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	Timeout *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=timeout,proto3" json:"timeout,omitempty"`
	// If generate_certificate was set in the request, this is the signed x509
	// certificate to use for your server. It will be renewed with the lease.
	Certificate []byte `protobuf:"bytes,4,opt,name=Certificate,proto3" json:"Certificate,omitempty"`
}

func (x *Lease) Reset() {
	*x = Lease{}
	if protoimpl.UnsafeEnabled {
		mi := &file_gate_service_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Lease) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Lease) ProtoMessage() {}

func (x *Lease) ProtoReflect() protoreflect.Message {
	mi := &file_gate_service_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Lease.ProtoReflect.Descriptor instead.
func (*Lease) Descriptor() ([]byte, []int) {
	return file_gate_service_proto_rawDescGZIP(), []int{2}
}

func (x *Lease) GetPattern() string {
	if x != nil {
		return x.Pattern
	}
	return ""
}

func (x *Lease) GetAddress() string {
	if x != nil {
		return x.Address
	}
	return ""
}

func (x *Lease) GetPort() uint32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *Lease) GetTimeout() *timestamppb.Timestamp {
	if x != nil {
		return x.Timeout
	}
	return nil
}

func (x *Lease) GetCertificate() []byte {
	if x != nil {
		return x.Certificate
	}
	return nil
}

var File_gate_service_proto protoreflect.FileDescriptor

var file_gate_service_proto_rawDesc = []byte{
	0x0a, 0x12, 0x67, 0x61, 0x74, 0x65, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1b, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x65, 0x6d, 0x70, 0x74, 0x79, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x22, 0x26, 0x0a, 0x08, 0x48, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x1a,
	0x0a, 0x08, 0x68, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x08, 0x68, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d, 0x65, 0x22, 0xdb, 0x01, 0x0a, 0x0f, 0x52,
	0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x18,
	0x0a, 0x07, 0x70, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x07, 0x70, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x12, 0x1d, 0x0a, 0x0a, 0x66, 0x69, 0x78, 0x65,
	0x64, 0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x09, 0x66, 0x69,
	0x78, 0x65, 0x64, 0x50, 0x6f, 0x72, 0x74, 0x12, 0x1a, 0x0a, 0x08, 0x68, 0x6f, 0x73, 0x74, 0x6e,
	0x61, 0x6d, 0x65, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x68, 0x6f, 0x73, 0x74, 0x6e,
	0x61, 0x6d, 0x65, 0x12, 0x23, 0x0a, 0x0d, 0x73, 0x74, 0x72, 0x69, 0x70, 0x5f, 0x70, 0x61, 0x74,
	0x74, 0x65, 0x72, 0x6e, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0c, 0x73, 0x74, 0x72, 0x69,
	0x70, 0x50, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x12, 0x1d, 0x0a, 0x0a, 0x61, 0x6c, 0x6c, 0x6f,
	0x77, 0x5f, 0x68, 0x74, 0x74, 0x70, 0x18, 0x05, 0x20, 0x01, 0x28, 0x08, 0x52, 0x09, 0x61, 0x6c,
	0x6c, 0x6f, 0x77, 0x48, 0x74, 0x74, 0x70, 0x12, 0x2f, 0x0a, 0x13, 0x63, 0x65, 0x72, 0x74, 0x69,
	0x66, 0x69, 0x63, 0x61, 0x74, 0x65, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x18, 0x04,
	0x20, 0x01, 0x28, 0x0c, 0x52, 0x12, 0x63, 0x65, 0x72, 0x74, 0x69, 0x66, 0x69, 0x63, 0x61, 0x74,
	0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x22, 0xa7, 0x01, 0x0a, 0x05, 0x4c, 0x65, 0x61,
	0x73, 0x65, 0x12, 0x18, 0x0a, 0x07, 0x70, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x07, 0x70, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x12, 0x18, 0x0a, 0x07,
	0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x61,
	0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x0d, 0x52, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x12, 0x34, 0x0a, 0x07, 0x74, 0x69,
	0x6d, 0x65, 0x6f, 0x75, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f,
	0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69,
	0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x07, 0x74, 0x69, 0x6d, 0x65, 0x6f, 0x75, 0x74,
	0x12, 0x20, 0x0a, 0x0b, 0x43, 0x65, 0x72, 0x74, 0x69, 0x66, 0x69, 0x63, 0x61, 0x74, 0x65, 0x18,
	0x04, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x0b, 0x43, 0x65, 0x72, 0x74, 0x69, 0x66, 0x69, 0x63, 0x61,
	0x74, 0x65, 0x32, 0x9e, 0x01, 0x0a, 0x06, 0x50, 0x6f, 0x72, 0x74, 0x61, 0x6c, 0x12, 0x26, 0x0a,
	0x08, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x12, 0x10, 0x2e, 0x52, 0x65, 0x67, 0x69,
	0x73, 0x74, 0x65, 0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x06, 0x2e, 0x4c, 0x65,
	0x61, 0x73, 0x65, 0x22, 0x00, 0x12, 0x19, 0x0a, 0x05, 0x52, 0x65, 0x6e, 0x65, 0x77, 0x12, 0x06,
	0x2e, 0x4c, 0x65, 0x61, 0x73, 0x65, 0x1a, 0x06, 0x2e, 0x4c, 0x65, 0x61, 0x73, 0x65, 0x22, 0x00,
	0x12, 0x1e, 0x0a, 0x0a, 0x55, 0x6e, 0x72, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x12, 0x06,
	0x2e, 0x4c, 0x65, 0x61, 0x73, 0x65, 0x1a, 0x06, 0x2e, 0x4c, 0x65, 0x61, 0x73, 0x65, 0x22, 0x00,
	0x12, 0x31, 0x0a, 0x0a, 0x4d, 0x79, 0x48, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x16,
	0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66,
	0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79, 0x1a, 0x09, 0x2e, 0x48, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d,
	0x65, 0x22, 0x00, 0x42, 0x20, 0x5a, 0x1e, 0x61, 0x73, 0x6b, 0x2e, 0x73, 0x79, 0x73, 0x74, 0x65,
	0x6d, 0x73, 0x2f, 0x64, 0x61, 0x65, 0x6d, 0x6f, 0x6e, 0x2f, 0x70, 0x6f, 0x72, 0x74, 0x61, 0x6c,
	0x2f, 0x67, 0x61, 0x74, 0x65, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_gate_service_proto_rawDescOnce sync.Once
	file_gate_service_proto_rawDescData = file_gate_service_proto_rawDesc
)

func file_gate_service_proto_rawDescGZIP() []byte {
	file_gate_service_proto_rawDescOnce.Do(func() {
		file_gate_service_proto_rawDescData = protoimpl.X.CompressGZIP(file_gate_service_proto_rawDescData)
	})
	return file_gate_service_proto_rawDescData
}

var file_gate_service_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_gate_service_proto_goTypes = []interface{}{
	(*Hostname)(nil),              // 0: Hostname
	(*RegisterRequest)(nil),       // 1: RegisterRequest
	(*Lease)(nil),                 // 2: Lease
	(*timestamppb.Timestamp)(nil), // 3: google.protobuf.Timestamp
	(*emptypb.Empty)(nil),         // 4: google.protobuf.Empty
}
var file_gate_service_proto_depIdxs = []int32{
	3, // 0: Lease.timeout:type_name -> google.protobuf.Timestamp
	1, // 1: Portal.Register:input_type -> RegisterRequest
	2, // 2: Portal.Renew:input_type -> Lease
	2, // 3: Portal.Unregister:input_type -> Lease
	4, // 4: Portal.MyHostname:input_type -> google.protobuf.Empty
	2, // 5: Portal.Register:output_type -> Lease
	2, // 6: Portal.Renew:output_type -> Lease
	2, // 7: Portal.Unregister:output_type -> Lease
	0, // 8: Portal.MyHostname:output_type -> Hostname
	5, // [5:9] is the sub-list for method output_type
	1, // [1:5] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_gate_service_proto_init() }
func file_gate_service_proto_init() {
	if File_gate_service_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_gate_service_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Hostname); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_gate_service_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RegisterRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_gate_service_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Lease); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_gate_service_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_gate_service_proto_goTypes,
		DependencyIndexes: file_gate_service_proto_depIdxs,
		MessageInfos:      file_gate_service_proto_msgTypes,
	}.Build()
	File_gate_service_proto = out.File
	file_gate_service_proto_rawDesc = nil
	file_gate_service_proto_goTypes = nil
	file_gate_service_proto_depIdxs = nil
}
