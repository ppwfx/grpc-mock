package main

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/fatih/structs"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/davecgh/go-spew/spew"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"

	"github.com/ppwfx/grpc-mock/grpc/pingpb"
)

func main() {
	err := run()
	if err != nil {
		log.Fatalln(err)
	}
}

func run() error {
	ms, err := LoadMocks("mocks")
	if err != nil {
		return errors.Wrapf(err, "failed to load mocks")
	}

	srv := grpc.NewServer(grpcmiddleware.WithUnaryServerChain(
		mockInterceptor(pingpb.UnimplementedPingServiceServer{}, ms),
	))

	pingpb.RegisterPingServiceServer(srv, pingpb.UnimplementedPingServiceServer{})

	ctx := context.Background()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	g, gtx := errgroup.WithContext(ctx)

	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		return errors.Wrapf(err, "failed to listen on :8080")
	}

	g.Go(func() error {
		err := srv.Serve(l)
		if err != nil {
			return errors.Wrapf(err, "failed ")
		}

		return nil
	})

	g.Go(func() error {
		<-gtx.Done()

		srv.Stop()

		return nil
	})

	d, err := grpc.Dial(":8080", grpc.WithInsecure())
	if err != nil {
		return errors.Wrapf(err, "failed to dial")
	}

	c := pingpb.NewPingServiceClient(d)

	rsp, err := c.Ping(ctx, &pingpb.PingRequest{
		Ping: "hello",
	})
	if err != nil {
		return errors.Wrapf(err, "failed to ping")
	}

	spew.Dump(rsp)

	cancel()

	err = g.Wait()
	if err != nil {
		return errors.Wrapf(err, "failed to wait")
	}

	return nil
}

func mockInterceptor(svc interface{}, mocks map[string][]Mock) func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	t := reflect.TypeOf(svc)

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		method := strings.TrimPrefix(info.FullMethod, "/ping.PingService/")

		m, ok := t.MethodByName(method)
		if !ok {
			return nil, errors.Errorf("did not find method=%v", method)
		}

		rsp := reflect.New(m.Type.Out(0).Elem()).Interface()

		err := mapMock(mocks[method], req, rsp)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to map mocks")
		}

		return rsp, nil
	}
}

func LoadMocks(dir string) (map[string][]Mock, error) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read dir=%v", dir)
	}

	mocks := map[string][]Mock{}
	for _, file := range files {
		f, err := os.Open(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, errors.Wrapf(err, "failed to open file=%v", file.Name())
		}

		m := []Mock{}
		err = json.NewDecoder(f).Decode(&m)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to decode mock with file=%v", file.Name())
		}

		mocks[strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))] = m
	}

	return mocks, nil
}

type Mock struct {
	Request    map[string]interface{}
	Response   map[string]interface{}
	StatusCode int
}

func mapMock(ms []Mock, req, rsp interface{}) error {
	s := structs.New(req)
	s.TagName = "json"

	for i := range ms {
		if !reflect.DeepEqual(ms[i].Request, s.Map()) {
			continue
		}

		err := mapstructure.Decode(ms[i].Response, &rsp)
		if err != nil {
			return errors.Wrapf(err, "failed to decode response")
		}

		return nil
	}

	return nil
}
