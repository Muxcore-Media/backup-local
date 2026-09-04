module github.com/Muxcore-Media/backup-local

go 1.26.4

require (
	github.com/Muxcore-Media/core v0.5.8
	github.com/Muxcore-Media/core/pkg/contracts v0.5.8
	github.com/Muxcore-Media/core/sdk/go/client v0.5.8
	github.com/Muxcore-Media/core/sdk/go/module v0.5.2
	github.com/robfig/cron/v3 v3.0.1
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

replace github.com/Muxcore-Media/core => ../core

replace github.com/Muxcore-Media/core/pkg/contracts => ../core/pkg/contracts

replace github.com/Muxcore-Media/core/sdk/go/module => ../core/sdk/go/module

replace github.com/Muxcore-Media/core/sdk/go/client => ../core/sdk/go/client

require (
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
)
