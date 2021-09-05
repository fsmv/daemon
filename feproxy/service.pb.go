// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.27.1
// 	protoc        v3.17.3
// source: service.proto

package feproxy

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
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

type RegisterRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Pattern string `protobuf:"bytes,1,opt,name=pattern,proto3" json:"pattern,omitempty"` // TODO: maybe support multiple patterns for the same IP/port
	// Set for third party web interfaces that can't take an automatic lease port
	// Must be outside the range of feproxy's automatic ports
	FixedPort uint32 `protobuf:"varint,2,opt,name=fixed_port,json=fixedPort,proto3" json:"fixed_port,omitempty"`
	// If true, remove the pattern in the URL of HTTP requests we forward to the
	// backend to hide that it is behind a reverse proxy.
	StripPattern bool `protobuf:"varint,3,opt,name=strip_pattern,json=stripPattern,proto3" json:"strip_pattern,omitempty"`
}

func (x *RegisterRequest) Reset() {
	*x = RegisterRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_service_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RegisterRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RegisterRequest) ProtoMessage() {}

func (x *RegisterRequest) ProtoReflect() protoreflect.Message {
	mi := &file_service_proto_msgTypes[0]
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
	return file_service_proto_rawDescGZIP(), []int{0}
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

func (x *RegisterRequest) GetStripPattern() bool {
	if x != nil {
		return x.StripPattern
	}
	return false
}

type Lease struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Pattern string                 `protobuf:"bytes,1,opt,name=pattern,proto3" json:"pattern,omitempty"`
	Port    uint32                 `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	Timeout *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=timeout,proto3" json:"timeout,omitempty"`
}

func (x *Lease) Reset() {
	*x = Lease{}
	if protoimpl.UnsafeEnabled {
		mi := &file_service_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Lease) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Lease) ProtoMessage() {}

func (x *Lease) ProtoReflect() protoreflect.Message {
	mi := &file_service_proto_msgTypes[1]
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
	return file_service_proto_rawDescGZIP(), []int{1}
}

func (x *Lease) GetPattern() string {
	if x != nil {
		return x.Pattern
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

// Just so this can be used as a json flag
type RegisterRequests struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Requests []*RegisterRequest `protobuf:"bytes,1,rep,name=requests,proto3" json:"requests,omitempty"`
}

func (x *RegisterRequests) Reset() {
	*x = RegisterRequests{}
	if protoimpl.UnsafeEnabled {
		mi := &file_service_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RegisterRequests) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RegisterRequests) ProtoMessage() {}

func (x *RegisterRequests) ProtoReflect() protoreflect.Message {
	mi := &file_service_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RegisterRequests.ProtoReflect.Descriptor instead.
func (*RegisterRequests) Descriptor() ([]byte, []int) {
	return file_service_proto_rawDescGZIP(), []int{2}
}

func (x *RegisterRequests) GetRequests() []*RegisterRequest {
	if x != nil {
		return x.Requests
	}
	return nil
}

var File_service_proto protoreflect.FileDescriptor

var file_service_proto_rawDesc = []byte{
	0x0a, 0x0d, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a,
	0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66,
	0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x22, 0x6f, 0x0a, 0x0f, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x52, 0x65, 0x71, 0x75,
	0x65, 0x73, 0x74, 0x12, 0x18, 0x0a, 0x07, 0x70, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x70, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x12, 0x1d, 0x0a,
	0x0a, 0x66, 0x69, 0x78, 0x65, 0x64, 0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0d, 0x52, 0x09, 0x66, 0x69, 0x78, 0x65, 0x64, 0x50, 0x6f, 0x72, 0x74, 0x12, 0x23, 0x0a, 0x0d,
	0x73, 0x74, 0x72, 0x69, 0x70, 0x5f, 0x70, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x08, 0x52, 0x0c, 0x73, 0x74, 0x72, 0x69, 0x70, 0x50, 0x61, 0x74, 0x74, 0x65, 0x72,
	0x6e, 0x22, 0x6b, 0x0a, 0x05, 0x4c, 0x65, 0x61, 0x73, 0x65, 0x12, 0x18, 0x0a, 0x07, 0x70, 0x61,
	0x74, 0x74, 0x65, 0x72, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x70, 0x61, 0x74,
	0x74, 0x65, 0x72, 0x6e, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x0d, 0x52, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x12, 0x34, 0x0a, 0x07, 0x74, 0x69, 0x6d, 0x65,
	0x6f, 0x75, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67,
	0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65,
	0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x07, 0x74, 0x69, 0x6d, 0x65, 0x6f, 0x75, 0x74, 0x22, 0x40,
	0x0a, 0x10, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x73, 0x12, 0x2c, 0x0a, 0x08, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x73, 0x18, 0x01,
	0x20, 0x03, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x52, 0x08, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x73,
	0x32, 0x6c, 0x0a, 0x07, 0x46, 0x65, 0x70, 0x72, 0x6f, 0x78, 0x79, 0x12, 0x26, 0x0a, 0x08, 0x52,
	0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x12, 0x10, 0x2e, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74,
	0x65, 0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x06, 0x2e, 0x4c, 0x65, 0x61, 0x73,
	0x65, 0x22, 0x00, 0x12, 0x19, 0x0a, 0x05, 0x52, 0x65, 0x6e, 0x65, 0x77, 0x12, 0x06, 0x2e, 0x4c,
	0x65, 0x61, 0x73, 0x65, 0x1a, 0x06, 0x2e, 0x4c, 0x65, 0x61, 0x73, 0x65, 0x22, 0x00, 0x12, 0x1e,
	0x0a, 0x0a, 0x55, 0x6e, 0x72, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x12, 0x06, 0x2e, 0x4c,
	0x65, 0x61, 0x73, 0x65, 0x1a, 0x06, 0x2e, 0x4c, 0x65, 0x61, 0x73, 0x65, 0x22, 0x00, 0x42, 0x10,
	0x5a, 0x0e, 0x64, 0x61, 0x65, 0x6d, 0x6f, 0x6e, 0x2f, 0x66, 0x65, 0x70, 0x72, 0x6f, 0x78, 0x79,
	0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_service_proto_rawDescOnce sync.Once
	file_service_proto_rawDescData = file_service_proto_rawDesc
)

func file_service_proto_rawDescGZIP() []byte {
	file_service_proto_rawDescOnce.Do(func() {
		file_service_proto_rawDescData = protoimpl.X.CompressGZIP(file_service_proto_rawDescData)
	})
	return file_service_proto_rawDescData
}

var file_service_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_service_proto_goTypes = []interface{}{
	(*RegisterRequest)(nil),       // 0: RegisterRequest
	(*Lease)(nil),                 // 1: Lease
	(*RegisterRequests)(nil),      // 2: RegisterRequests
	(*timestamppb.Timestamp)(nil), // 3: google.protobuf.Timestamp
}
var file_service_proto_depIdxs = []int32{
	3, // 0: Lease.timeout:type_name -> google.protobuf.Timestamp
	0, // 1: RegisterRequests.requests:type_name -> RegisterRequest
	0, // 2: Feproxy.Register:input_type -> RegisterRequest
	1, // 3: Feproxy.Renew:input_type -> Lease
	1, // 4: Feproxy.Unregister:input_type -> Lease
	1, // 5: Feproxy.Register:output_type -> Lease
	1, // 6: Feproxy.Renew:output_type -> Lease
	1, // 7: Feproxy.Unregister:output_type -> Lease
	5, // [5:8] is the sub-list for method output_type
	2, // [2:5] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_service_proto_init() }
func file_service_proto_init() {
	if File_service_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_service_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
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
		file_service_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
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
		file_service_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RegisterRequests); i {
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
			RawDescriptor: file_service_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_service_proto_goTypes,
		DependencyIndexes: file_service_proto_depIdxs,
		MessageInfos:      file_service_proto_msgTypes,
	}.Build()
	File_service_proto = out.File
	file_service_proto_rawDesc = nil
	file_service_proto_goTypes = nil
	file_service_proto_depIdxs = nil
}
