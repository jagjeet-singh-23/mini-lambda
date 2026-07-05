module github.com/jagjeet-singh-23/mini-lambda/services/gateway

go 1.25.7

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/jagjeet-singh-23/mini-lambda/shared v0.0.0
	github.com/prometheus/client_golang v1.23.2
	github.com/redis/go-redis/v9 v9.18.0
	golang.org/x/time v0.14.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/gofrs/uuid/v5 v5.4.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jagjeet-singh-23/mini-lambda v0.0.0-20260131094956-20beb0782307 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/sys v0.39.0 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
)

replace github.com/jagjeet-singh-23/mini-lambda/shared => ../../shared
