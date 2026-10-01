package main

import (
	"context"
	app "github.com/laudryfadian/griyo-backend-service-workspace/internal"
	"github.com/laudryfadian/griyo-backend-service-workspace/internal/pb"
	"github.com/laudryfadian/griyo-backend-service-workspace/internal/platform"
	"log"
)

func main() {
	ctx := context.Background()
	db, e := platform.Db(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	conn, e := platform.Dial(platform.Env("TASK_GRPC_ADDR", "task:50053"))
	if e != nil {
		log.Fatal(e)
	}
	defer conn.Close()
	svc, e := app.New(ctx, db, pb.NewTaskServiceClient(conn))
	if e != nil {
		log.Fatal(e)
	}
	server := platform.Server()
	pb.RegisterWorkspaceServiceServer(server, svc)
	log.Fatal(platform.ServeGrpc(server, platform.Env("GRPC_ADDR", ":50051")))
}
