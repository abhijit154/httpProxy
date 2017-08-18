package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	redis "gopkg.in/redis.v3"

	"github.com/creack/goproxy/registry"
	"github.com/valyala/fasthttp"
)

var (
	addr               = flag.String("addr", ":6330", "TCP address to listen to")
	compress           = flag.Bool("compress", false, "Whether to enable transparent response compression")
	ErrInvalidService  = errors.New("Invalid service/version")
	ExtractNameVersion = extractNameVersion
	LoadBalance        = loadBalance
)

var fasthttpClient = &fasthttp.Client{
	MaxConnsPerHost:     DefaultConnections,
	MaxIdleConnDuration: DefaultConnectionTimeout,
	ReadTimeout:         DefaultTimeout,
	WriteTimeout:        DefaultTimeout,
	TLSConfig: &tls.Config{
		InsecureSkipVerify: true,
		ClientSessionCache: tls.NewLRUClientSessionCache(0),
	},
}

const (
	// DefaultTimeout is the default amount of time an Attacker waits for a request before it times out.
	DefaultTimeout = 120 * time.Second
	// DefaultConnections is the default amount of max open idle connections per target host.
	DefaultConnections       = 15000
	DefaultConnectionTimeout = 30 * time.Second
)

var ServiceRegistry = registry.DefaultRegistry{
	"service1": {
		"Key": {
			"http://abcMock.com",
			"https://abcActual.com",
		},
	},
}

var Client = redis.NewClient(&redis.Options{
	Addr:     "localhost:6379",
	Password: "", // no password set
	DB:       0,  // use default DB
})

func main() {
	flag.Parse()
	fmt.Println("Inside Main")
	h := requestHandler
	if *compress {
		h = fasthttp.CompressHandler(h)
	}



	if err := fasthttp.ListenAndServe(*addr, h); err != nil {
		log.Fatalf("Error in ListenAndServe: %s", err)
	}
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	fmt.Println("Inside requestHandler")
	serviceName, serviceVersion, uriT, errService := extractNameVersion(string(ctx.Request.RequestURI()))
	fmt.Println("Service Name ", serviceName, " \n Service Version ", serviceVersion)
	checkError(errService)
	endpointT, errEndpoint := loadBalance(serviceName, serviceVersion, ServiceRegistry)
	if endpointT == "" {
		ctx.SetContentType("application/json")
		ctx.Response.SetBodyString("{\"status\": { \"statusType\": \"ERROR\", \"statusMessage\": \"service name/version not found\" }}")
		ctx.Response.SetStatusCode(http.StatusBadRequest)
		return
	}
	fmt.Println(endpointT, errEndpoint, uriT)
	checkError(errEndpoint)
	respFromTarget := HitTarget(ctx, endpointT, uriT)
	ctx.SetContentType(string(respFromTarget.Header.Peek("content-type")))
	fmt.Println("Response Body", string(respFromTarget.Body()));
	ctx.Response.SetBody(respFromTarget.Body())
	ctx.Response.SetStatusCode(respFromTarget.StatusCode())
}

func HitTarget(ctx *fasthttp.RequestCtx, targetEndPoint, targetURI string) *fasthttp.Response {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	var Url *url.URL
	Url, err := url.Parse(targetEndPoint + targetURI)
	checkError(err)
	req.SetRequestURI(Url.String())
	fmt.Println("Request Method",string(ctx.Method()))
	req.Header.SetMethod(string(ctx.Method()))



	//Parse all headers from original request
	headersFromRequest := string(ctx.Request.Header.Header())
	for index, value := range strings.Split(headersFromRequest, "\n") {
		if index == 0 || strings.Contains(value, "Host") || strings.Contains(value, "User-Agent") || strings.Contains(value, "Postman-Token") || strings.Contains(value, "Origin") || strings.Contains(value, "Content-Length") {
			continue
		}
		headersKV := strings.SplitN(value, ":", 2)
		if len(headersKV) == 2 {
			//fmt.Println(headersKV[0], ":", headersKV[1])
			req.Header.Set(strings.TrimSpace(headersKV[0]), strings.TrimSpace(headersKV[1]))
		}
	}

	// For Get and Delete
	if ctx.IsGet() || ctx.IsDelete() {
		fmt.Println("Inside GET and DELTE")
		err := fasthttpClient.Do(req, resp)
		checkError(err)
		fmt.Println("Response ", resp)
		return resp
	}

	//For Post and PUT Requests
	if ctx.IsPost() || ctx.IsPut() {
		fmt.Println("Inside POST and PUT")
		req.Header.Set("Content-Type", "text/xml")
		req.SetBodyString(string(ctx.Request.Body()))

		err := fasthttpClient.Do(req, resp)
		checkError(err)
		fmt.Println("Response ", resp)
		return resp
	}

	fmt.Println("Response From Out Side ......", resp)
	return resp
}

func extractNameVersion(path string) (name, version, targetPath string, err error) {
	fmt.Println("PATH : ", path)
	//path := target.Path
	if len(path) > 1 && path[0] == '/' {
		path = path[1:]
	}
	tmp := strings.Split(path, "/")
	if len(tmp) < 2 {
		return "", "", "", fmt.Errorf("Invalid path")
	}
	name, version = tmp[0], tmp[1]
	targetPath = "/" + strings.Join(tmp[2:], "/")
	return name, version, targetPath, nil
}

func loadBalance(serviceName, serviceVersion string, reg registry.Registry) (string, error) {
	var endpoint string
	endpoints, err := reg.Lookup(serviceName, serviceVersion)
	fmt.Println("End Points", endpoints)
	if err != nil {
		return "", err
	}
	for {
		// No more endpoint, stop
		if len(endpoints) == 0 {
			break
		}
		_, err := Client.Ping().Result()
		val, err := Client.Get(serviceVersion).Result()
		if err != nil {
			val = "false"
			//panic(err)
			fmt.Println(err)
		}
		fmt.Println("Value from redis key : ", val)
		if b, _ := strconv.ParseBool(val); b {
			endpoint = endpoints[0]
		} else {
			endpoint = endpoints[1]
		}
		return endpoint, nil
	}
	return "", fmt.Errorf("No endpoint available for %s/%s", serviceName, serviceVersion)
}

func checkError(err error) {
	if err != nil {
		fmt.Println("Error", err)
	}
}

