package controller

import (
	"context"
	"github.com/golang/protobuf/ptypes/empty"
	pb "mq-subscriber/api"
)

func (c *controller) All(_ context.Context, _ *empty.Empty) (*pb.IDs, error) {
	return nil, nil
}