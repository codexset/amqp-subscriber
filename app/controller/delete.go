package controller

import (
	"context"
	pb "mq-subscriber/router"
)

func (c *controller) Delete(ctx context.Context, param *pb.DeleteParameter) (*pb.Response, error) {
	err := c.manager.Delete(param.Identity)
	if err != nil {
		return c.response(err)
	}
	return c.response(nil)
}