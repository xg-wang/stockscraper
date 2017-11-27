package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly"
)

// Time is our customized type to override UnmarshalJSON interface
type Time struct {
	time.Time
}

// UnmarshalJSON implements the json.Unmarshaler interface.
// The time is expected to be a quoted string in stocktwit's strange format.
func (t *Time) UnmarshalJSON(data []byte) error {
	strData := strings.Trim(string(data), "\"")
	// Ignore null, like in the main JSON package.
	if strData == "null" {
		return nil
	}
	form := "Mon, 02 Jan 2006 15:04:05 -0000"
	time, err := time.Parse(form, strData)
	if err != nil {
		return err
	}
	*t = Time{time}
	return nil
}

// Message represents a message extracted from stocktwits.com
type Message struct {
	ID        int    `json:"id"`
	Body      string `json:"body"`
	CreatedAt Time   `json:"created_at"`
	Sentiment struct {
		Class string `json:"class"`
		Name  string `json:"name"`
	} `json:"sentiment"`
	TotalLikes int `json:"total_likes"`
}

// Stream is the response type of stocktwits
// Since, Max is the id of Message, Max is actually smaller id
type Stream struct {
	More     bool      `json:"more"`
	Since    int64     `json:"since,omitempty"`
	Max      int64     `json:"max,omitempty"`
	Messages []Message `json:"messages"`
}

type scrapeInfos struct {
	symbol    string
	csrfToken string
	id        int
	wg        sync.WaitGroup
}

var (
	logger *log.Logger
	infos  *scrapeInfos
	c      *colly.Collector
)

// Send request to retrieve data
func pollMessages(url string, csrfToken string) error {
	infos.wg.Wait()
	time.Sleep(time.Second)

	hdr := http.Header{}
	hdr.Set("x-csrf-token", csrfToken)
	hdr.Set("x-requested-with", "XMLHttpRequest")
	logger.Printf("ready to send request: %s\n%v\n", url, hdr)
	return c.Request("GET", url, nil, nil, hdr)
}

func main() {
	logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
	logger.SetPrefix("\n")
	// logger := log.New(ioutil.Discard, "", log.Ldate|log.Ltime|log.Lshortfile)

	var symbol = flag.String("symbol", "AAPL", "symbol to look for")
	flag.Parse()
	fName := fmt.Sprintf("%s-msg.csv", *symbol)
	file, err := os.Create(fName)
	if err != nil {
		logger.Fatalf("Cannot create file %q: %s\n", fName, err)
		return
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write CSV header
	writer.Write([]string{"ID", "Body", "CreatedAt", "Sentiment", "Likes", "Replies"})

	messages := make([]*Message, 0)
	done := make(chan bool, 0)

	// Instantiate default collector
	c = colly.NewCollector()
	// c.SetDebugger(&debug.LogDebugger{})
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*stocktwits.com/streams",
		Parallelism: 2,
		Delay:       2 * time.Second,
	})
	c.UserAgent = "Mozilla/5.0 (Windows NT 6.1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/41.0.2228.0 Safari/537.36"

	// Extract infos for request
	infos = &scrapeInfos{symbol: *symbol}
	infos.wg.Add(2)
	c.OnHTML("meta[name=csrf-token]", func(e *colly.HTMLElement) {
		defer infos.wg.Done()
		infos.csrfToken = e.Attr("content")
		if infos.csrfToken == "" {
			logger.Fatalf("csrf token not found")
		}
		logger.Printf("csrfToken is %s\n", infos.csrfToken)
	})
	c.OnHTML("ol.stream-list", func(e *colly.HTMLElement) {
		defer infos.wg.Done()
		infos.id, err = strconv.Atoi(e.Attr("stream-id"))
		if err != nil {
			logger.Fatalf("id not found")
		}
		logger.Printf("id is %d\n", infos.id)
	})

	go func() {
		infos.wg.Wait()
		url := fmt.Sprintf("https://stocktwits.com/streams/stream?stream=symbol&stream_id=%d&substream=all&username=undefined&symbol=undefined", infos.id)
		err := pollMessages(url, infos.csrfToken)
		if err != nil {
			logger.Fatal(err)
		}
	}()

	c.OnRequest(func(r *colly.Request) {
		logger.Printf("URL    : %s\n", r.URL)
		// logger.Printf("Headers: %v\n", r.Headers)
	})

	c.OnResponse(func(r *colly.Response) {
		logger.Printf("Response Headers: %v\n", r.Headers)
		if strings.Index(r.Headers.Get("Content-Type"), "json") == -1 {
			return
		}
		data := Stream{}
		err := json.Unmarshal(r.Body, &data)
		if err != nil {
			logger.Fatal(err)
		}
		logger.Print(data)
	})

	c.OnError(func(_ *colly.Response, err error) {
		logger.Printf("ERROR: %s", err)
	})

	c.Visit(fmt.Sprintf("https://stocktwits.com/symbol/%s", infos.symbol))

	<-done

	fmt.Print(messages)
}
