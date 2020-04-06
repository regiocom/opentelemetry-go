// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package grpctrace

// gRPC tracing middleware
// https://github.com/open-telemetry/opentelemetry-specification/blob/master/specification/data-rpc.md
import (
	"context"
	"net"
	"regexp"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"go.opentelemetry.io/otel/api/core"
	"go.opentelemetry.io/otel/api/correlation"
	"go.opentelemetry.io/otel/api/key"
	"go.opentelemetry.io/otel/api/trace"
)

var (
	rpcServiceKey  = key.New("rpc.service")
	netPeerIpKey   = key.New("net.peer.ip")
	netPeerNameKey = key.New("net.peer.name")
	netPeerPortKey = key.New("net.peer.port")
)

func UnaryClientInterceptor(tracer trace.Tracer) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		requestMetadata, _ := metadata.FromOutgoingContext(ctx)
		metadataCopy := requestMetadata.Copy()

		var span trace.Span
		ctx, span = tracer.Start(
			ctx, method,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(peerInfoFromTarget(cc.Target())...),
			trace.WithAttributes(rpcServiceKey.String(serviceFromFullMethod(method))),
		)
		defer span.End()

		Inject(ctx, &metadataCopy)
		ctx = metadata.NewOutgoingContext(ctx, metadataCopy)

		err := invoker(ctx, method, req, reply, cc, opts...)

		if err != nil {
			s, _ := status.FromError(err)
			span.SetStatus(s.Code(), s.Message())
		}

		return err
	}
}

// clientStream  wraps around the embedded grpc.ClientStream, and intercepts the RecvMsg and
// SendMsg method call.
type clientStream struct {
	grpc.ClientStream

	//finished chan error
	//
	//clientClosed    chan struct{}
	//receiveFinished chan struct{}
}

func (w *clientStream) RecvMsg(m interface{}) error {
	err := w.ClientStream.RecvMsg(m)

	//if err == io.EOF {
	//	w.receiveFinished <- struct{}{}
	//} else if err != nil {
	//	w.finished <- err
	//}

	return err
}

func (w *clientStream) SendMsg(m interface{}) error {
	return w.ClientStream.SendMsg(m)
}

func (w *clientStream) CloseSend() error {
	err := w.ClientStream.CloseSend()
	//
	//if err != nil {
	//	w.finished <- err
	//} else {
	//	w.clientClosed <- struct{}{}
	//}

	return err
}

func wrapClientStream(s grpc.ClientStream, desc *grpc.StreamDesc) *clientStream {
	//clientClosed := make(chan struct{})
	//receiveFinished := make(chan struct{})
	//
	//finished := make(chan error)
	//
	//go func() {
	//	if desc.ServerStreams {
	//		<-receiveFinished
	//	}
	//
	//	<-clientClosed
	//
	//	finished <- nil
	//}()

	return &clientStream{
		ClientStream:    s,
		//finished:        finished,
		//clientClosed:    clientClosed,
		//receiveFinished: receiveFinished,
	}
}

// streamInterceptor is an example stream interceptor.
func StreamClientInterceptor(tracer trace.Tracer) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		requestMetadata, _ := metadata.FromOutgoingContext(ctx)
		metadataCopy := requestMetadata.Copy()

		var span trace.Span
		ctx, span = tracer.Start(
			ctx, method,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(peerInfoFromTarget(cc.Target())...),
			trace.WithAttributes(rpcServiceKey.String(serviceFromFullMethod(method))),
		)

		Inject(ctx, &metadataCopy)
		ctx = metadata.NewOutgoingContext(ctx, metadataCopy)

		s, err := streamer(ctx, desc, cc, method, opts...)
		stream := wrapClientStream(s, desc)

		go func() {
			if err == nil {
				<- s.Context().Done()
				err = s.Context().Err()
				
			}

			if err != nil {
				s, _ := status.FromError(err)
				span.SetStatus(s.Code(), s.Message())
			}

			span.End()
		}()

		return stream, err
	}
}

func UnaryServerInterceptor(tracer trace.Tracer) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		requestMetadata, _ := metadata.FromIncomingContext(ctx)
		metadataCopy := requestMetadata.Copy()

		entries, spanCtx := Extract(ctx, &metadataCopy)
		ctx = correlation.ContextWithMap(ctx, correlation.NewMap(correlation.MapUpdate{
			MultiKV: entries,
		}))

		ctx, span := tracer.Start(
			trace.ContextWithRemoteSpanContext(ctx, spanCtx),
			info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(peerInfoFromContext(ctx)...),
			trace.WithAttributes(rpcServiceKey.String(serviceFromFullMethod(info.FullMethod))),
		)
		defer span.End()

		resp, err := handler(ctx, req)

		if err != nil {
			s, _ := status.FromError(err)
			span.SetStatus(s.Code(), s.Message())
		}

		return resp, err
	}
}

// clientStream wraps around the embedded grpc.ServerStream, and intercepts the RecvMsg and
// SendMsg method call.
type serverStream struct {
	grpc.ServerStream
}

func (w *serverStream) RecvMsg(m interface{}) error {
	return w.ServerStream.RecvMsg(m)
}

func (w *serverStream) SendMsg(m interface{}) error {
	return w.ServerStream.SendMsg(m)
}

func StreamServerInterceptor(tracer trace.Tracer) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()

		requestMetadata, _ := metadata.FromIncomingContext(ctx)
		metadataCopy := requestMetadata.Copy()

		entries, spanCtx := Extract(ctx, &metadataCopy)
		ctx = correlation.ContextWithMap(ctx, correlation.NewMap(correlation.MapUpdate{
			MultiKV: entries,
		}))

		ctx, span := tracer.Start(
			trace.ContextWithRemoteSpanContext(ctx, spanCtx),
			info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(peerInfoFromContext(ctx)...),
			trace.WithAttributes(rpcServiceKey.String(serviceFromFullMethod(info.FullMethod))),
		)
		defer span.End()

		err := handler(srv, &serverStream{ss})

		if err != nil {
			s, _ := status.FromError(err)
			span.SetStatus(s.Code(), s.Message())
		}

		return err
	}
}

func peerInfoFromTarget(target string) []core.KeyValue {
	host, port, err := net.SplitHostPort(target)

	if err != nil {
		return []core.KeyValue{}
	}

	if host == "" {
		host = "127.0.0.1"
	}

	return []core.KeyValue{
		netPeerIpKey.String(host),
		netPeerPortKey.String(port),
	}
}

func peerInfoFromContext(ctx context.Context) []core.KeyValue {
	p, ok := peer.FromContext(ctx)

	if !ok {
		return []core.KeyValue{}
	}

	return peerInfoFromTarget(p.Addr.String())
}

var fullMethodRegexp = regexp.MustCompile(`^/\S*\.(\S*)/\S*$`)

func serviceFromFullMethod(method string) string {
	match := fullMethodRegexp.FindAllStringSubmatch(method, 1)

	if len(match) != 1 && len(match[1]) != 2 {
		return ""
	}

	return match[0][1]
}
