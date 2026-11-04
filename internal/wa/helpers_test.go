package wa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// syntheticKey is deterministic test data, never an account credential.
func syntheticKey() [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

type forbiddenTransport struct{}

func (forbiddenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected external network request")
}

// fakeProvider serves only its injected response and never falls back to a network.
type fakeProvider struct {
	body string
}

func (p fakeProvider) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(p.body)),
	}, nil
}

type testBarrier struct {
	entered chan struct{}
	release chan struct{}
}

func newTestBarrier() *testBarrier {
	return &testBarrier{entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *testBarrier) wait(ctx context.Context) error {
	close(b.entered)
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestOfflineFixtures(t *testing.T) {
	_ = t.TempDir()
	if syntheticKey() != syntheticKey() {
		t.Fatal("synthetic key is not deterministic")
	}
	client := &http.Client{Transport: forbiddenTransport{}}
	if _, err := client.Get("https://example.invalid/"); err == nil {
		t.Fatal("forbidden transport allowed a request")
	}
	client.Transport = fakeProvider{body: "synthetic"}
	resp, err := client.Get("https://example.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "synthetic" {
		t.Fatalf("fake response = %q, %v", body, err)
	}
	b := newTestBarrier()
	result := make(chan error, 1)
	go func() { result <- b.wait(context.Background()) }()
	<-b.entered
	close(b.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
