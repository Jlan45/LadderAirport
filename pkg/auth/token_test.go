package auth_test

import (
	"context"
	"testing"

	"github.com/ladderairport/pkg/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAppendToken(t *testing.T) {
	ctx := auth.AppendBearerToken(context.Background(), "secret")
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("missing metadata")
	}
	vals := md.Get("authorization")
	if len(vals) != 1 || vals[0] != "Bearer secret" {
		t.Fatalf("got %v", vals)
	}
}

func TestValidateBearer(t *testing.T) {
	md := metadata.Pairs("authorization", "Bearer secret")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	if err := auth.ValidateIncomingBearer(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := auth.ValidateIncomingBearer(ctx, "wrong"); err == nil {
		t.Fatal("expected error")
	}
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *fakeServerStream) Context() context.Context {
	return s.ctx
}

func TestStreamServerInterceptor(t *testing.T) {
	interceptor := auth.StreamServerInterceptor("secret")

	md := metadata.Pairs("authorization", "Bearer secret")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ss := &fakeServerStream{ctx: ctx}

	called := false
	err := interceptor(nil, ss, &grpc.StreamServerInfo{}, func(srv any, stream grpc.ServerStream) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("handler not called")
	}

	// Wrong token should fail before handler
	badMD := metadata.Pairs("authorization", "Bearer wrong")
	badCtx := metadata.NewIncomingContext(context.Background(), badMD)
	badSS := &fakeServerStream{ctx: badCtx}
	err = interceptor(nil, badSS, &grpc.StreamServerInfo{}, func(srv any, stream grpc.ServerStream) error {
		t.Fatal("handler should not be called")
		return nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestUnaryServerInterceptor(t *testing.T) {
	interceptor := auth.UnaryServerInterceptor("secret")

	md := metadata.Pairs("authorization", "Bearer secret")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	called := false
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("handler not called")
	}

	// Wrong token should fail
	badMD := metadata.Pairs("authorization", "Bearer wrong")
	badCtx := metadata.NewIncomingContext(context.Background(), badMD)
	_, err = interceptor(badCtx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		t.Fatal("handler should not be called")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}
