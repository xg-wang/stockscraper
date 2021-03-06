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
	"github.com/gocolly/colly/debug"
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
	ID        int64  `json:"id"`
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
	delay     time.Duration
	wg        sync.WaitGroup
	mutex     sync.Mutex
}

var (
	logger *log.Logger
	infos  *scrapeInfos
	c      *colly.Collector
)

// Send request to retrieve data
func pollMessages(url string, csrfToken string) error {
	infos.wg.Wait()
	time.Sleep(infos.delay * time.Millisecond)

	hdr := http.Header{}
	hdr.Set("x-csrf-token", csrfToken)
	hdr.Set("x-requested-with", "XMLHttpRequest")
	// logger.Printf("ready to send request: %s\n%v\n", url, hdr)
	return c.Request("GET", url, nil, nil, hdr)
}

func main() {
	logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
	logger.SetPrefix("\n")
	// logger := log.New(ioutil.Discard, "", log.Ldate|log.Ltime|log.Lshortfile)

	var symbol = flag.String("symbol", "AAPL", "symbol to look for")
	var maxDateStr = flag.String("date", "2014-11-11", "earliest date for data, default to 2014-11-11")
	var maxID = flag.Int64("id", 0, "restart from maxID")
	var delay = flag.Int64("delay", 500, "delay ms between request, default 500")
	var retry = flag.Int("retry", 5, "retry request if failed, default 5, -1 for unlimited")
	flag.Parse()
	retryRemain := *retry

	fName := fmt.Sprintf("%s.csv", *symbol)
	maxDate, err := time.Parse("2006-01-02", *maxDateStr)
	if err != nil {
		logger.Fatal(err)
	}
	file, err := os.OpenFile(fName, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		logger.Fatalf("Cannot open file %q: %s\n", fName, err)
		return
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	writer.Comma = '\t'
	defer writer.Flush()

	// Write CSV header
	stat, err := file.Stat()
	if err != nil {
		logger.Fatal(err)
	}
	// write head line if none
	if stat.Size() < 40 {
		writer.Write([]string{"Id", "CreatedAt", "Body", "Sentiment", "Likes"})
	}

	done := sync.WaitGroup{}
	done.Add(1)

	// Instantiate default collector
	c = colly.NewCollector()
	c.SetDebugger(&debug.LogDebugger{})
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*stocktwits.com/streams",
		Parallelism: 2,
		Delay:       2 * time.Second,
	})
	c.UserAgent = "Mozilla/5.0 (Windows NT 6.1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/41.0.2228.0 Safari/537.36"

	// Extract infos for request
	infos = &scrapeInfos{symbol: *symbol, delay: time.Duration(*delay)}
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
		if *maxID != 0 {
			url = fmt.Sprintf("https://stocktwits.com/streams/poll?stream=symbol&stream_id=%d&substream=all&max=%d", infos.id, *maxID)
		}
		err := pollMessages(url, infos.csrfToken)
		if err != nil {
			logger.Println(err)
		}
	}()

	c.OnRequest(func(r *colly.Request) {
		logger.Printf("URL    : %s\n", r.URL)
		// logger.Printf("Headers: %v\n", r.Headers)
	})

	c.OnResponse(func(r *colly.Response) {
		// reset retry once succeed
		retryRemain = *retry
		// logger.Printf("Response Headers: %v\n", r.Headers)
		if strings.Index(r.Headers.Get("Content-Type"), "json") == -1 {
			return
		}
		data := Stream{}
		err := json.Unmarshal(r.Body, &data)
		if err != nil {
			logger.Fatal(err)
		}
		if len(data.Messages) == 0 {
			logger.Println("receiving 0 messages, exit...")
			defer done.Done()
			return
		}
		if data.Since == 0 || data.Max == 0 {
			data.Since = data.Messages[0].ID
			data.Max = data.Messages[len(data.Messages)-1].ID
		}
		logger.Printf("Response got %d messages, %d - %d\n", len(data.Messages), data.Since, data.Max)
		go func() {
			url := fmt.Sprintf("https://stocktwits.com/streams/poll?stream=symbol&stream_id=%d&substream=all&max=%d", infos.id, data.Max)
			err := pollMessages(url, infos.csrfToken)
			if err != nil {
				logger.Println(err)
			}
		}()
		infos.mutex.Lock()
		done.Add(1)
		for _, msg := range data.Messages {
			sentiment := "Neutral"
			if msg.Sentiment.Name != "" {
				sentiment = msg.Sentiment.Name
			}
			msg.Body = strings.Replace(msg.Body, "\n", "\\n", -1)
			msg.Body = strings.Replace(msg.Body, "\t", " ", -1)
			writer.Write(
				[]string{
					strconv.FormatInt(msg.ID, 10), msg.CreatedAt.Format(time.RFC3339), msg.Body,
					sentiment, strconv.Itoa(msg.TotalLikes)})
		}
		done.Done()
		infos.mutex.Unlock()
		// end condition
		if data.Messages[len(data.Messages)-1].CreatedAt.Before(maxDate) {
			done.Done()
		}
	})

	c.OnError(func(res *colly.Response, err error) {
		if retryRemain == 0 {
			logger.Fatal("exit due to request failure.")
		}
		retryRemain--
		logger.Print("ERROR: retrying..." + strconv.Itoa(*retry-retryRemain))
		res.Request.Retry()
	})

	c.Visit(fmt.Sprintf("https://stocktwits.com/symbol/%s", infos.symbol))

	done.Wait()
}
