package auth

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const MDAuthorization = "authorization"

func AppendBearerToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, MDAuthorization, "Bearer "+token)
}

func ValidateIncomingBearer(ctx context.Context, expected string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "缺少请求元数据")
	}
	vals := md.Get(MDAuthorization)
	if len(vals) == 0 {
		return status.Error(codes.Unauthenticated, "缺少身份认证信息")
	}
	raw := vals[0]
	const prefix = "Bearer "
	if !strings.HasPrefix(raw, prefix) {
		return status.Error(codes.Unauthenticated, "身份认证方案无效")
	}
	got := strings.TrimPrefix(raw, prefix)
	if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		return status.Error(codes.Unauthenticated, "令牌无效")
	}
	return nil
}

func UnaryServerInterceptor(expectedToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := ValidateIncomingBearer(ctx, expectedToken); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func StreamServerInterceptor(expectedToken string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := ValidateIncomingBearer(ss.Context(), expectedToken); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
