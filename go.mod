module ask.systems/daemon

go 1.17

replace ask.systems/daemon/portal => ./portal

replace ask.systems/daemon/host => ./host

replace ask.systems/daemon/tools => ./tools

replace ask.systems/daemon/assimilate => ./assimilate

replace ask.systems/daemon/spawn => ./spawn

require (
	ask.systems/daemon/assimilate v0.0.0-00010101000000-000000000000
	ask.systems/daemon/host v0.0.0-00010101000000-000000000000
	ask.systems/daemon/portal v0.0.0-00010101000000-000000000000
	ask.systems/daemon/spawn v0.0.0-00010101000000-000000000000
	ask.systems/daemon/tools v0.0.0-00010101000000-000000000000
)

require (
	github.com/golang/protobuf v1.5.0 // indirect
	golang.org/x/net v0.0.0-20200822124328-c89045814202 // indirect
	golang.org/x/sys v0.0.0-20200323222414-85ca7c5b95cd // indirect
	golang.org/x/text v0.3.0 // indirect
	google.golang.org/genproto v0.0.0-20200526211855-cb27e3aa2013 // indirect
	google.golang.org/grpc v1.40.0 // indirect
	google.golang.org/protobuf v1.27.1 // indirect
)
