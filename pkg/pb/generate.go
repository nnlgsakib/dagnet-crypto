package pb

//go:generate protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative ../../proto/*.proto

// This file enables go generate to create protobuf code