package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client reports one chat server's address and load to the registry.
type Client struct {
	baseURL       *url.URL
	serverAddress string
	httpClient    *http.Client
}

type loadRequest struct {
	Address string `json:"address"`
	Load    int    `json:"load"`
}

const maxErrorBodyBytes = 4 << 10

// NewClient validates and records the registry connection details.
func NewClient(baseURL, serverAddress string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("registry base URL must not be empty")
	}

	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse registry base URL %q: %w", baseURL, err)
	}
	if parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https" {
		return nil, fmt.Errorf("registry base URL has unsupported scheme %q", parsedBaseURL.Scheme)
	}
	if parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("registry base URL must include a host")
	}
	if parsedBaseURL.User != nil {
		return nil, fmt.Errorf("registry base URL must not include credentials")
	}
	if parsedBaseURL.Path != "" && parsedBaseURL.Path != "/" {
		return nil, fmt.Errorf("registry base URL must not include a path")
	}
	if parsedBaseURL.RawQuery != "" {
		return nil, fmt.Errorf("registry base URL must not include a query")
	}
	if parsedBaseURL.Fragment != "" {
		return nil, fmt.Errorf("registry base URL must not include a fragment")
	}

	trimmedServerAddress := strings.TrimSpace(serverAddress)
	if trimmedServerAddress == "" {
		return nil, fmt.Errorf("registry server address must not be empty")
	}
	if trimmedServerAddress != serverAddress {
		return nil, fmt.Errorf("registry server address must not contain surrounding whitespace")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("registry HTTP client must not be nil")
	}

	parsedBaseURL.Path = ""
	return &Client{
		baseURL:       parsedBaseURL,
		serverAddress: serverAddress,
		httpClient:    httpClient,
	}, nil
}

// Register announces the chat server with an initial load of zero.
func (c *Client) Register(ctx context.Context) error {
	return c.post(ctx, "/register", 0)
}

// UpdateLoad reports the current number of connected clients.
func (c *Client) UpdateLoad(ctx context.Context, load int) error {
	if load < 0 {
		return fmt.Errorf("registry load must not be negative: %d", load)
	}
	return c.post(ctx, "/update", load)
}

func (c *Client) post(ctx context.Context, path string, load int) error {
	payload, err := json.Marshal(loadRequest{
		Address: c.serverAddress,
		Load:    load,
	})
	if err != nil {
		return fmt.Errorf("encode registry request: %w", err)
	}

	endpoint := *c.baseURL
	endpoint.Path = path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create registry request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send registry request to %s: %w", path, err)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
		closeErr := response.Body.Close()

		truncated := len(body) > maxErrorBodyBytes
		if truncated {
			body = body[:maxErrorBodyBytes]
		}
		bodyText := strings.TrimSpace(string(body))
		statusErr := fmt.Errorf("registry request to %s returned %s", path, response.Status)
		if bodyText != "" {
			if truncated {
				statusErr = fmt.Errorf("%w: response body %q (truncated)", statusErr, bodyText)
			} else {
				statusErr = fmt.Errorf("%w: response body %q", statusErr, bodyText)
			}
		}
		if readErr != nil {
			statusErr = errors.Join(statusErr, fmt.Errorf("read registry error response body: %w", readErr))
		}
		if closeErr != nil {
			statusErr = errors.Join(statusErr, fmt.Errorf("close registry response body: %w", closeErr))
		}
		return statusErr
	}

	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		readErr = fmt.Errorf("read successful registry response body: %w", readErr)
		if closeErr != nil {
			return errors.Join(readErr, fmt.Errorf("close registry response body: %w", closeErr))
		}
		return readErr
	}
	if closeErr != nil {
		return fmt.Errorf("close registry response body: %w", closeErr)
	}

	return nil
}
