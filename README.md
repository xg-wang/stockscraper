# stockscrape

Scrape messages frorm [stocktwits](http://stocktwits.com/) based on [Colly](http://go-colly.org/) framework.

# Usage

```bash
go get github.com/xg-wang/stockscraper
```

```plain
Usage of ./scrape:
  -date string
    	earliest date for data, default to 2014-11-11 (default "2014-11-11")
  -delay int
    	delay ms between request, default 500 (default 500)
  -id int
    	restart from maxID
  -retry int
    	retry request if failed, default 5, -1 for unlimited (default 5)
  -symbol string
    	symbol to look for (default "AAPL")
```
