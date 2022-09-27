// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        v3.20.1
// source: embedspawn/config.proto

package embedspawn

import (
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

// # Example config file (by the way textproto supports comments)
// command {
// filepath: "host"
// user: "www"
// no_chroot: true
// args: [
// "--web_root /home/www/",
// "--url_path /files/"
// ]
// }
type Config struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Command []*Command `protobuf:"bytes,1,rep,name=command,proto3" json:"command,omitempty"`
}

func (x *Config) Reset() {
	*x = Config{}
	if protoimpl.UnsafeEnabled {
		mi := &file_embedspawn_config_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Config) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Config) ProtoMessage() {}

func (x *Config) ProtoReflect() protoreflect.Message {
	mi := &file_embedspawn_config_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Config.ProtoReflect.Descriptor instead.
func (*Config) Descriptor() ([]byte, []int) {
	return file_embedspawn_config_proto_rawDescGZIP(), []int{0}
}

func (x *Config) GetCommand() []*Command {
	if x != nil {
		return x.Command
	}
	return nil
}

// Next ID: 10
type Command struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Binary is the absolute path to the executable file or the relative
	// path within the directory provided in the -path flag.
	//
	// Required.
	Binary string `protobuf:"bytes,1,opt,name=binary,proto3" json:"binary,omitempty"`
	// User to run the process as. Cannot be root.
	//
	// Required.
	User string `protobuf:"bytes,3,opt,name=user,proto3" json:"user,omitempty"`
	// Additional name to show in the dashboard to keep logs separate
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// If unset, cd and/or chroot into $HOME, otherwise use this directory
	WorkingDir string `protobuf:"bytes,8,opt,name=working_dir,json=workingDir,proto3" json:"working_dir,omitempty"`
	// Set to true if you don't want the binary run in chroot at working_dir
	NoChroot bool `protobuf:"varint,7,opt,name=no_chroot,json=noChroot,proto3" json:"no_chroot,omitempty"`
	// Args is the arguments to pass to the executable
	Args []string `protobuf:"bytes,4,rep,name=args,proto3" json:"args,omitempty"`
	// Ports to listen on (with tcp) and pass to the process as files.
	// Useful for accessing the privelaged ports (<1024).
	//
	// In the child process, the sockets will have fd = 3 + i, where Ports[i] is
	// the port to bind
	Ports []uint32 `protobuf:"varint,5,rep,packed,name=ports,proto3" json:"ports,omitempty"`
	// Files to open and pass to the process
	//
	// In the child process, the files will have fd = 3 + len(Ports) + i, where
	// Files[i] is the file
	Files []string `protobuf:"bytes,6,rep,name=files,proto3" json:"files,omitempty"`
	// Set to true if all of the files are tls certs you want to keep
	// autoupdated. Portal has an -auto_tls_cert fag to support reading this.
	//
	// This makes the files in the above array a pipe that will be updated with
	// the file contents on startup and when spawn in sent the SIGUSR1 signal.
	//
	// To use this run the following command after renewing your cert:
	//
	//	killall -SIGUSR1 {portal,spawn}
	AutoTlsCerts bool `protobuf:"varint,9,opt,name=auto_tls_certs,json=autoTlsCerts,proto3" json:"auto_tls_certs,omitempty"`
}

func (x *Command) Reset() {
	*x = Command{}
	if protoimpl.UnsafeEnabled {
		mi := &file_embedspawn_config_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Command) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Command) ProtoMessage() {}

func (x *Command) ProtoReflect() protoreflect.Message {
	mi := &file_embedspawn_config_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Command.ProtoReflect.Descriptor instead.
func (*Command) Descriptor() ([]byte, []int) {
	return file_embedspawn_config_proto_rawDescGZIP(), []int{1}
}

func (x *Command) GetBinary() string {
	if x != nil {
		return x.Binary
	}
	return ""
}

func (x *Command) GetUser() string {
	if x != nil {
		return x.User
	}
	return ""
}

func (x *Command) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Command) GetWorkingDir() string {
	if x != nil {
		return x.WorkingDir
	}
	return ""
}

func (x *Command) GetNoChroot() bool {
	if x != nil {
		return x.NoChroot
	}
	return false
}

func (x *Command) GetArgs() []string {
	if x != nil {
		return x.Args
	}
	return nil
}

func (x *Command) GetPorts() []uint32 {
	if x != nil {
		return x.Ports
	}
	return nil
}

func (x *Command) GetFiles() []string {
	if x != nil {
		return x.Files
	}
	return nil
}

func (x *Command) GetAutoTlsCerts() bool {
	if x != nil {
		return x.AutoTlsCerts
	}
	return false
}

var File_embedspawn_config_proto protoreflect.FileDescriptor

var file_embedspawn_config_proto_rawDesc = []byte{
	0x0a, 0x17, 0x65, 0x6d, 0x62, 0x65, 0x64, 0x73, 0x70, 0x61, 0x77, 0x6e, 0x2f, 0x63, 0x6f, 0x6e,
	0x66, 0x69, 0x67, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x2c, 0x0a, 0x06, 0x43, 0x6f, 0x6e,
	0x66, 0x69, 0x67, 0x12, 0x22, 0x0a, 0x07, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x18, 0x01,
	0x20, 0x03, 0x28, 0x0b, 0x32, 0x08, 0x2e, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x52, 0x07,
	0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x22, 0xed, 0x01, 0x0a, 0x07, 0x43, 0x6f, 0x6d, 0x6d,
	0x61, 0x6e, 0x64, 0x12, 0x16, 0x0a, 0x06, 0x62, 0x69, 0x6e, 0x61, 0x72, 0x79, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x06, 0x62, 0x69, 0x6e, 0x61, 0x72, 0x79, 0x12, 0x12, 0x0a, 0x04, 0x75,
	0x73, 0x65, 0x72, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x75, 0x73, 0x65, 0x72, 0x12,
	0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x12, 0x1f, 0x0a, 0x0b, 0x77, 0x6f, 0x72, 0x6b, 0x69, 0x6e, 0x67, 0x5f, 0x64,
	0x69, 0x72, 0x18, 0x08, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x77, 0x6f, 0x72, 0x6b, 0x69, 0x6e,
	0x67, 0x44, 0x69, 0x72, 0x12, 0x1b, 0x0a, 0x09, 0x6e, 0x6f, 0x5f, 0x63, 0x68, 0x72, 0x6f, 0x6f,
	0x74, 0x18, 0x07, 0x20, 0x01, 0x28, 0x08, 0x52, 0x08, 0x6e, 0x6f, 0x43, 0x68, 0x72, 0x6f, 0x6f,
	0x74, 0x12, 0x12, 0x0a, 0x04, 0x61, 0x72, 0x67, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x09, 0x52,
	0x04, 0x61, 0x72, 0x67, 0x73, 0x12, 0x14, 0x0a, 0x05, 0x70, 0x6f, 0x72, 0x74, 0x73, 0x18, 0x05,
	0x20, 0x03, 0x28, 0x0d, 0x52, 0x05, 0x70, 0x6f, 0x72, 0x74, 0x73, 0x12, 0x14, 0x0a, 0x05, 0x66,
	0x69, 0x6c, 0x65, 0x73, 0x18, 0x06, 0x20, 0x03, 0x28, 0x09, 0x52, 0x05, 0x66, 0x69, 0x6c, 0x65,
	0x73, 0x12, 0x24, 0x0a, 0x0e, 0x61, 0x75, 0x74, 0x6f, 0x5f, 0x74, 0x6c, 0x73, 0x5f, 0x63, 0x65,
	0x72, 0x74, 0x73, 0x18, 0x09, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0c, 0x61, 0x75, 0x74, 0x6f, 0x54,
	0x6c, 0x73, 0x43, 0x65, 0x72, 0x74, 0x73, 0x42, 0x25, 0x5a, 0x23, 0x61, 0x73, 0x6b, 0x2e, 0x73,
	0x79, 0x73, 0x74, 0x65, 0x6d, 0x73, 0x2f, 0x64, 0x61, 0x65, 0x6d, 0x6f, 0x6e, 0x2f, 0x73, 0x70,
	0x61, 0x77, 0x6e, 0x2f, 0x65, 0x6d, 0x62, 0x65, 0x64, 0x73, 0x70, 0x61, 0x77, 0x6e, 0x62, 0x06,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_embedspawn_config_proto_rawDescOnce sync.Once
	file_embedspawn_config_proto_rawDescData = file_embedspawn_config_proto_rawDesc
)

func file_embedspawn_config_proto_rawDescGZIP() []byte {
	file_embedspawn_config_proto_rawDescOnce.Do(func() {
		file_embedspawn_config_proto_rawDescData = protoimpl.X.CompressGZIP(file_embedspawn_config_proto_rawDescData)
	})
	return file_embedspawn_config_proto_rawDescData
}

var file_embedspawn_config_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_embedspawn_config_proto_goTypes = []interface{}{
	(*Config)(nil),  // 0: Config
	(*Command)(nil), // 1: Command
}
var file_embedspawn_config_proto_depIdxs = []int32{
	1, // 0: Config.command:type_name -> Command
	1, // [1:1] is the sub-list for method output_type
	1, // [1:1] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_embedspawn_config_proto_init() }
func file_embedspawn_config_proto_init() {
	if File_embedspawn_config_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_embedspawn_config_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Config); i {
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
		file_embedspawn_config_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Command); i {
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
			RawDescriptor: file_embedspawn_config_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_embedspawn_config_proto_goTypes,
		DependencyIndexes: file_embedspawn_config_proto_depIdxs,
		MessageInfos:      file_embedspawn_config_proto_msgTypes,
	}.Build()
	File_embedspawn_config_proto = out.File
	file_embedspawn_config_proto_rawDesc = nil
	file_embedspawn_config_proto_goTypes = nil
	file_embedspawn_config_proto_depIdxs = nil
}
