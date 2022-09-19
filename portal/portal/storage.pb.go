// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        v3.20.1
// source: portal/storage.proto

package main

import (
	portal "ask.systems/daemon/portal"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Registration struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Request *portal.RegisterRequest `protobuf:"bytes,1,opt,name=request,proto3" json:"request,omitempty"`
	// Note: the Certificate field is not filled in the stored copy because it is
	// not passed through. It could have been done but it wasn't necessary.
	Lease      *portal.Lease `protobuf:"bytes,2,opt,name=lease,proto3" json:"lease,omitempty"`
	ClientAddr string        `protobuf:"bytes,3,opt,name=client_addr,json=clientAddr,proto3" json:"client_addr,omitempty"`
}

func (x *Registration) Reset() {
	*x = Registration{}
	if protoimpl.UnsafeEnabled {
		mi := &file_portal_storage_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Registration) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Registration) ProtoMessage() {}

func (x *Registration) ProtoReflect() protoreflect.Message {
	mi := &file_portal_storage_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Registration.ProtoReflect.Descriptor instead.
func (*Registration) Descriptor() ([]byte, []int) {
	return file_portal_storage_proto_rawDescGZIP(), []int{0}
}

func (x *Registration) GetRequest() *portal.RegisterRequest {
	if x != nil {
		return x.Request
	}
	return nil
}

func (x *Registration) GetLease() *portal.Lease {
	if x != nil {
		return x.Lease
	}
	return nil
}

func (x *Registration) GetClientAddr() string {
	if x != nil {
		return x.ClientAddr
	}
	return ""
}

type State struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Registrations []*Registration `protobuf:"bytes,1,rep,name=registrations,proto3" json:"registrations,omitempty"`
	RootCAs       [][]byte        `protobuf:"bytes,2,rep,name=rootCAs,proto3" json:"rootCAs,omitempty"`
}

func (x *State) Reset() {
	*x = State{}
	if protoimpl.UnsafeEnabled {
		mi := &file_portal_storage_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *State) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*State) ProtoMessage() {}

func (x *State) ProtoReflect() protoreflect.Message {
	mi := &file_portal_storage_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use State.ProtoReflect.Descriptor instead.
func (*State) Descriptor() ([]byte, []int) {
	return file_portal_storage_proto_rawDescGZIP(), []int{1}
}

func (x *State) GetRegistrations() []*Registration {
	if x != nil {
		return x.Registrations
	}
	return nil
}

func (x *State) GetRootCAs() [][]byte {
	if x != nil {
		return x.RootCAs
	}
	return nil
}

var File_portal_storage_proto protoreflect.FileDescriptor

var file_portal_storage_proto_rawDesc = []byte{
	0x0a, 0x14, 0x70, 0x6f, 0x72, 0x74, 0x61, 0x6c, 0x2f, 0x73, 0x74, 0x6f, 0x72, 0x61, 0x67, 0x65,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x0d, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x79, 0x0a, 0x0c, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x72,
	0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x2a, 0x0a, 0x07, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65,
	0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x52, 0x07, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x12, 0x1c, 0x0a, 0x05, 0x6c, 0x65, 0x61, 0x73, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x06, 0x2e, 0x4c, 0x65, 0x61, 0x73, 0x65, 0x52, 0x05, 0x6c, 0x65, 0x61, 0x73, 0x65, 0x12,
	0x1f, 0x0a, 0x0b, 0x63, 0x6c, 0x69, 0x65, 0x6e, 0x74, 0x5f, 0x61, 0x64, 0x64, 0x72, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x63, 0x6c, 0x69, 0x65, 0x6e, 0x74, 0x41, 0x64, 0x64, 0x72,
	0x22, 0x56, 0x0a, 0x05, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x33, 0x0a, 0x0d, 0x72, 0x65, 0x67,
	0x69, 0x73, 0x74, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b,
	0x32, 0x0d, 0x2e, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x52,
	0x0d, 0x72, 0x65, 0x67, 0x69, 0x73, 0x74, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x12, 0x18,
	0x0a, 0x07, 0x72, 0x6f, 0x6f, 0x74, 0x43, 0x41, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0c, 0x52,
	0x07, 0x72, 0x6f, 0x6f, 0x74, 0x43, 0x41, 0x73, 0x42, 0x20, 0x5a, 0x1e, 0x61, 0x73, 0x6b, 0x2e,
	0x73, 0x79, 0x73, 0x74, 0x65, 0x6d, 0x73, 0x2f, 0x64, 0x61, 0x65, 0x6d, 0x6f, 0x6e, 0x2f, 0x70,
	0x6f, 0x72, 0x74, 0x61, 0x6c, 0x2f, 0x6d, 0x61, 0x69, 0x6e, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x33,
}

var (
	file_portal_storage_proto_rawDescOnce sync.Once
	file_portal_storage_proto_rawDescData = file_portal_storage_proto_rawDesc
)

func file_portal_storage_proto_rawDescGZIP() []byte {
	file_portal_storage_proto_rawDescOnce.Do(func() {
		file_portal_storage_proto_rawDescData = protoimpl.X.CompressGZIP(file_portal_storage_proto_rawDescData)
	})
	return file_portal_storage_proto_rawDescData
}

var file_portal_storage_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_portal_storage_proto_goTypes = []interface{}{
	(*Registration)(nil),           // 0: Registration
	(*State)(nil),                  // 1: State
	(*portal.RegisterRequest)(nil), // 2: RegisterRequest
	(*portal.Lease)(nil),           // 3: Lease
}
var file_portal_storage_proto_depIdxs = []int32{
	2, // 0: Registration.request:type_name -> RegisterRequest
	3, // 1: Registration.lease:type_name -> Lease
	0, // 2: State.registrations:type_name -> Registration
	3, // [3:3] is the sub-list for method output_type
	3, // [3:3] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_portal_storage_proto_init() }
func file_portal_storage_proto_init() {
	if File_portal_storage_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_portal_storage_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Registration); i {
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
		file_portal_storage_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*State); i {
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
			RawDescriptor: file_portal_storage_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_portal_storage_proto_goTypes,
		DependencyIndexes: file_portal_storage_proto_depIdxs,
		MessageInfos:      file_portal_storage_proto_msgTypes,
	}.Build()
	File_portal_storage_proto = out.File
	file_portal_storage_proto_rawDesc = nil
	file_portal_storage_proto_goTypes = nil
	file_portal_storage_proto_depIdxs = nil
}
