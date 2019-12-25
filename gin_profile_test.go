package gin

import (
	"crypto/tls"
	"fmt"
	"github.com/gin-gonic/gin/internal/profile"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"math/rand"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestProfile(t *testing.T) {
	router := New()
	max := float64(100000000000)
	router.Handle(http.MethodGet, "/test", func(c *Context) {
		rNum := rand.Intn(100) * 1024
		tmp := make([]float64, rNum)
		for i := 0; i < len(tmp); i++ {
			tmp[i] = rand.Float64() + 1
		}
		for count := 0; count < 10000; count++ {
			for i := 0; i < len(tmp); i++ {
				tmp[i] = tmp[i] * tmp[i]
				if tmp[i] > max {
					tmp[i] -= max
				}
			}
		}
		c.String(http.StatusOK, "it worked")
	})
	assert.NoError(t, EnablePeriodicallyProfile(&profile.Option{
		Y:          10 * time.Second,
		X:          3 * time.Second,
		StoreDir:   "/tmp/profiles",
		Compress:   true,
		MaxFileNum: 100,
	}, profile.Cpu, profile.Goroutine))
	go func() {
		router.GET("/example", func(c *Context) { c.String(http.StatusOK, "it worked") })
		assert.NoError(t, router.Run(":5150"))
	}()
	go func() {
		testConcurrentRequest(t, "http://localhost:5150/test", 4)
	}()
	time.Sleep(24 * time.Second)
	fmt.Println("Sleep finished")
}

func testConcurrentRequest(t *testing.T, url string, concurrency int) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}

	wa := sync.WaitGroup{}
	for {
		wa.Add(concurrency)
		for i := 0; i < concurrency; i++ {
			go func() {
				doRequest(t, client, url)
				wa.Done()
			}()
		}
		wa.Wait()
		fmt.Printf("finished %d requests\n", concurrency)
	}
}
func doRequest(t *testing.T, client *http.Client, url string) {
	resp, err := client.Get(url)
	assert.NoError(t, err)
	defer resp.Body.Close()
	body, ioerr := ioutil.ReadAll(resp.Body)
	assert.NoError(t, ioerr)
	assert.Equal(t, "it worked", string(body), "resp body should match")
	assert.Equal(t, "200 OK", resp.Status, "should get a 200")
}
