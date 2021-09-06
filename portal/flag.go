package portal

import (
    "google.golang.org/protobuf/encoding/protojson"
)

func (req *RegisterRequest) Set(input string) error {
    return protojson.Unmarshal([]byte(input), req)
}

func (reqs *RegisterRequests) Set(input string) error {
    return protojson.Unmarshal([]byte(input), reqs)
}
