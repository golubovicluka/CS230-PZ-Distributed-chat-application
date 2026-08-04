package loadbalancer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMutationEndpointsRejectInvalidRequests(t *testing.T) {
	oversizedBody := `{"address":"` + strings.Repeat("a", int(maxRequestBodyBytes)) + `","load":0}`
	testCases := []struct {
		name       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{name: "empty body", body: "", wantStatus: http.StatusBadRequest, wantBody: "invalid request body"},
		{name: "malformed JSON", body: `{`, wantStatus: http.StatusBadRequest, wantBody: "invalid request body"},
		{name: "unknown field", body: `{"address":"server-a","load":0,"weight":1}`, wantStatus: http.StatusBadRequest, wantBody: "invalid request body"},
		{name: "second JSON value", body: `{"address":"server-a","load":0}{"address":"server-b","load":1}`, wantStatus: http.StatusBadRequest, wantBody: "invalid request body"},
		{name: "missing address", body: `{"load":0}`, wantStatus: http.StatusBadRequest, wantBody: "address is required"},
		{name: "blank address", body: `{"address":"  ","load":0}`, wantStatus: http.StatusBadRequest, wantBody: "address is required"},
		{name: "negative load", body: `{"address":"server-a","load":-1}`, wantStatus: http.StatusBadRequest, wantBody: "load must not be negative"},
		{name: "wrong load type", body: `{"address":"server-a","load":"one"}`, wantStatus: http.StatusBadRequest, wantBody: "invalid request body"},
		{name: "oversized body", body: oversizedBody, wantStatus: http.StatusRequestEntityTooLarge, wantBody: "request body is too large"},
	}

	for _, endpoint := range []string{"/register", "/update"} {
		for _, testCase := range testCases {
			t.Run(strings.TrimPrefix(endpoint, "/")+"/"+testCase.name, func(t *testing.T) {
				balancer, _ := newTestLoadBalancer()
				request := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(testCase.body))
				response := httptest.NewRecorder()

				balancer.Handler().ServeHTTP(response, request)

				if response.Code != testCase.wantStatus {
					t.Fatalf("status = %d, want %d; body = %q", response.Code, testCase.wantStatus, response.Body.String())
				}
				if !strings.Contains(response.Body.String(), testCase.wantBody) {
					t.Errorf("body = %q, want it to contain %q", response.Body.String(), testCase.wantBody)
				}

				getRequest := httptest.NewRequest(http.MethodGet, "/get", nil)
				getResponse := httptest.NewRecorder()
				balancer.Handler().ServeHTTP(getResponse, getRequest)
				if getResponse.Code != http.StatusServiceUnavailable {
					t.Errorf("GET /get after rejected request = %d, want %d", getResponse.Code, http.StatusServiceUnavailable)
				}
			})
		}
	}
}

func TestMutationEndpointsUpsertAndSelect(t *testing.T) {
	type mutation struct {
		endpoint string
		address  string
		load     int
	}
	testCases := []struct {
		name       string
		mutations  []mutation
		wantServer serverResponse
	}{
		{
			name: "register selects least loaded",
			mutations: []mutation{
				{endpoint: "/register", address: "server-a", load: 5},
				{endpoint: "/register", address: "server-b", load: 2},
			},
			wantServer: serverResponse{Address: "server-b", Load: 2},
		},
		{
			name: "register replaces existing server",
			mutations: []mutation{
				{endpoint: "/register", address: "server-a", load: 8},
				{endpoint: "/register", address: "server-b", load: 4},
				{endpoint: "/register", address: "server-a", load: 1},
			},
			wantServer: serverResponse{Address: "server-a", Load: 1},
		},
		{
			name: "update inserts missing server",
			mutations: []mutation{
				{endpoint: "/update", address: "server-a", load: 3},
			},
			wantServer: serverResponse{Address: "server-a", Load: 3},
		},
		{
			name: "update replaces existing load",
			mutations: []mutation{
				{endpoint: "/register", address: "server-a", load: 7},
				{endpoint: "/register", address: "server-b", load: 4},
				{endpoint: "/update", address: "server-a", load: 2},
			},
			wantServer: serverResponse{Address: "server-a", Load: 2},
		},
		{
			name: "equal load uses lexical address",
			mutations: []mutation{
				{endpoint: "/register", address: "server-z", load: 2},
				{endpoint: "/register", address: "server-a", load: 2},
			},
			wantServer: serverResponse{Address: "server-a", Load: 2},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			balancer, _ := newTestLoadBalancer()
			handler := balancer.Handler()
			for _, mutation := range testCase.mutations {
				payload, err := json.Marshal(serverRequest{Address: mutation.address, Load: mutation.load})
				if err != nil {
					t.Fatalf("marshal request: %v", err)
				}
				request := httptest.NewRequest(http.MethodPost, mutation.endpoint, bytes.NewReader(payload))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("POST %s status = %d, want %d; body = %q", mutation.endpoint, response.Code, http.StatusOK, response.Body.String())
				}
			}

			request := httptest.NewRequest(http.MethodGet, "/get", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("GET /get status = %d, want %d; body = %q", response.Code, http.StatusOK, response.Body.String())
			}
			if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
				t.Errorf("Content-Type = %q, want %q", got, want)
			}

			wantBody := fmt.Sprintf("{\"Address\":%q,\"load\":%d}\n", testCase.wantServer.Address, testCase.wantServer.Load)
			if response.Body.String() != wantBody {
				t.Errorf("body = %q, want %q", response.Body.String(), wantBody)
			}
		})
	}
}

func TestGetWithoutServersReturnsServiceUnavailable(t *testing.T) {
	balancer, _ := newTestLoadBalancer()
	request := httptest.NewRequest(http.MethodGet, "/get", nil)
	response := httptest.NewRecorder()

	balancer.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(response.Body.String(), "no available servers") {
		t.Errorf("body = %q, want unavailable-server message", response.Body.String())
	}
}

func TestHandlerUsesExplicitMethodRoutes(t *testing.T) {
	testCases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantAllow  string
	}{
		{name: "register rejects GET", method: http.MethodGet, path: "/register", wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost},
		{name: "update rejects PUT", method: http.MethodPut, path: "/update", wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost},
		{name: "get rejects POST", method: http.MethodPost, path: "/get", wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodGet},
		{name: "unknown path", method: http.MethodGet, path: "/missing", wantStatus: http.StatusNotFound},
		{name: "trailing slash is not a route", method: http.MethodGet, path: "/get/", wantStatus: http.StatusNotFound},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			balancer, _ := newTestLoadBalancer()
			request := httptest.NewRequest(testCase.method, testCase.path, nil)
			response := httptest.NewRecorder()

			balancer.Handler().ServeHTTP(response, request)

			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, testCase.wantStatus)
			}
			if testCase.wantAllow != "" && !strings.Contains(response.Header().Get("Allow"), testCase.wantAllow) {
				t.Errorf("Allow = %q, want it to contain %q", response.Header().Get("Allow"), testCase.wantAllow)
			}
		})
	}
}

func TestConcurrentRequests(t *testing.T) {
	balancer, _ := newTestLoadBalancer()
	server := httptest.NewServer(balancer.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 5 * time.Second}

	seedRequest, err := http.NewRequest(http.MethodPost, server.URL+"/register", strings.NewReader(`{"address":"seed","load":0}`))
	if err != nil {
		t.Fatalf("build seed request: %v", err)
	}
	if err := doRequest(client, seedRequest, http.StatusOK); err != nil {
		t.Fatalf("seed load balancer: %v", err)
	}

	const (
		workers    = 24
		iterations = 30
	)
	start := make(chan struct{})
	errorsFromWorkers := make(chan error, workers)
	var workersDone sync.WaitGroup
	workersDone.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer workersDone.Done()
			<-start

			for iteration := 0; iteration < iterations; iteration++ {
				endpoint := "/update"
				if iteration%2 == 0 {
					endpoint = "/register"
				}
				body := fmt.Sprintf(`{"address":"server-%02d","load":%d}`, worker, iteration)
				mutationRequest, err := http.NewRequest(http.MethodPost, server.URL+endpoint, strings.NewReader(body))
				if err != nil {
					errorsFromWorkers <- fmt.Errorf("worker %d build mutation request: %w", worker, err)
					return
				}
				if err := doRequest(client, mutationRequest, http.StatusOK); err != nil {
					errorsFromWorkers <- fmt.Errorf("worker %d mutation %d: %w", worker, iteration, err)
					return
				}

				getRequest, err := http.NewRequest(http.MethodGet, server.URL+"/get", nil)
				if err != nil {
					errorsFromWorkers <- fmt.Errorf("worker %d build get request: %w", worker, err)
					return
				}
				if err := doRequest(client, getRequest, http.StatusOK); err != nil {
					errorsFromWorkers <- fmt.Errorf("worker %d get %d: %w", worker, iteration, err)
					return
				}
			}
		}(worker)
	}

	close(start)
	workersDone.Wait()
	close(errorsFromWorkers)
	for err := range errorsFromWorkers {
		t.Error(err)
	}
}

type blockingLogWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (writer *blockingLogWriter) Write(payload []byte) (int, error) {
	writer.once.Do(func() {
		close(writer.started)
	})
	<-writer.release
	return len(payload), nil
}

func TestHandlerReleasesStateLockBeforeLogging(t *testing.T) {
	logWriter := &blockingLogWriter{started: make(chan struct{}), release: make(chan struct{})}
	balancer := New(log.New(logWriter, "", 0))
	request := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"address":"server-a","load":1}`))
	response := httptest.NewRecorder()
	requestDone := make(chan struct{})
	go func() {
		balancer.Handler().ServeHTTP(response, request)
		close(requestDone)
	}()

	select {
	case <-logWriter.started:
	case <-time.After(time.Second):
		close(logWriter.release)
		<-requestDone
		t.Fatal("handler did not reach logger")
	}

	stateWriteDone := make(chan struct{})
	go func() {
		balancer.upsert(serverInfo{address: "server-b", load: 2})
		close(stateWriteDone)
	}()

	lockReleased := false
	select {
	case <-stateWriteDone:
		lockReleased = true
	case <-time.After(time.Second):
	}
	close(logWriter.release)
	<-requestDone
	if !lockReleased {
		<-stateWriteDone
		t.Error("state mutex remained locked while the handler logged")
	}
}

func doRequest(client *http.Client, request *http.Request, wantStatus int) error {
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read response body: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close response body: %w", closeErr)
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("status = %d, want %d", response.StatusCode, wantStatus)
	}
	return nil
}

func newTestLoadBalancer() (*LoadBalancer, *bytes.Buffer) {
	logs := &bytes.Buffer{}
	return New(log.New(logs, "", 0)), logs
}
