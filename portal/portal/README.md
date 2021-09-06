This is the server binary for feproxy.

It is in a folder called feproxy because when you run `go build` or `go install`
it uses the folder name for the output binary name. I wanted to make it easy to
go install the server and also easy to import the client library. I guess I
decide the import path of the client library is more important.
