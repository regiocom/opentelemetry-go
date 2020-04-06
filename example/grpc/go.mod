module go.opentelemetry.io/otel/example/grpc

go 1.13

replace go.opentelemetry.io/otel => ../..

require (
	github.com/golang/protobuf v1.3.2
	go.opentelemetry.io/otel v0.4.2
	go.opentelemetry.io/otel/exporters/trace/jaeger v0.4.2
	golang.org/x/net v0.0.0-20190503192946-f4e77d36d62c
	google.golang.org/grpc v1.27.1
)
