package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Centny/gwf/util"
	"github.com/boltdb/bolt"
	"log"
	"net/http"
	"os"
	"time"
)

const (
	bucketName = "report_bucket"
)

var (
	url        string
	dur        string
	dbFile     string
	port       string
	ignoreFile string
	clear      bool
	viewOnly   bool
)

var (
	db        *bolt.DB
	ignoreMap map[string]bool
)

func init() {
	flag.StringVar(&url, "u", "", "url to get report")
	flag.StringVar(&dur, "d", "10s", "duration to get report from server, default 10s")
	flag.StringVar(&dbFile, "f", "report.db", "file name to store report data using boltdb, default report.db")
	flag.StringVar(&port, "p", "9000", "port to listen for report data, default 9000")
	flag.StringVar(&ignoreFile, "ignore", "./ignore.json", "file content to ignore name for report, default ignore.json")
	flag.BoolVar(&clear, "c", false, "clear old report data, defalt false")
	flag.BoolVar(&viewOnly, "v", false, "only view report, no need to get report from server")
}

func main() {
	flag.Parse()

	if url == "" {
		flag.Usage()
		os.Exit(1)
	}

	duration, err := time.ParseDuration(dur)
	if err != nil {
		fmt.Printf("illegal duration string: %v\n", dur)
		os.Exit(1)
	}

	err = initDB()
	if err != nil {
		panic(err)
	}
	defer db.Close()

	err = initIgnoreFile()
	if err != nil {
		panic(err)
	}
	if !viewOnly {
		go func() {
			ticker := time.NewTicker(duration)
			defer ticker.Stop()
			for {
				<-ticker.C
				res, err := requestData()
				if err != nil {
					log.Printf("request report from url: %v, err: %v\n", url, err)
					break
				}
				//handle report
				err = handleReport(res)
				if err != nil {
					log.Printf("handle report err: %v\n", err)
					break
				}
			}
		}()
	}

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/", fs)
	fmt.Printf("listen on :%v\n", port)
	http.HandleFunc("/report", getReport)
	http.HandleFunc("/list", getAllKeys)
	http.HandleFunc("/one", getOneReport)
	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		panic(err)
	}
}

func initIgnoreFile() error {
	f, err := os.Open(ignoreFile)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	var values []string
	err = dec.Decode(&values)
	if err != nil {
		return err
	}

	ignoreMap = make(map[string]bool)
	for i := range values {
		ignoreMap[values[i]] = true
	}
	log.Printf("ignore: %v\n", util.S2Json(values))

	return nil
}

func getReport(res http.ResponseWriter, r *http.Request) {
	data, err := report()
	if err != nil {
		log.Printf("get report error: %v\n", err)
		return
	}

	res.Header().Set("Content-Type", "application/json;charset=utf-8")
	buf, err := json.Marshal(handleReportResult(data))
	if err != nil {
		log.Printf("marshal json error: %v\n", err)
	}
	res.Write(buf)
}

func getOneReport(res http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	key := r.FormValue("key")

	var values []util.Map
	err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		err := decode(bucket.Get([]byte(key)), &values)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Println(err)
	}

	var x, y []int64
	for _, v := range values {
		x = append(x, v.IntVal("time"))
		y = append(y, v.IntVal("avg"))
	}

	res.Header().Set("Content-Type", "application/json;charset=utf-8")
	buf, err := json.Marshal(util.Map{"series": []util.Map{
		{"data": y, "type": "line", "name": key},
	}, "xAxis": handleXAxis(x)})
	if err != nil {
		log.Printf("marshal json error: %v\n", err)
	}
	res.Write(buf)
}
func requestData() (util.Map, error) {
	return util.HGet2(url)
}

func handleReport(data util.Map) error {
	if len(data) == 0 {
		return util.Err("data is empty")
	}
	now := util.Now()
	httpUsed := data.AryMapValP("http/used")
	err := db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		for _, item := range httpUsed {
			_, ignore := ignoreMap[item.StrVal("name")]
			if ignore {
				continue
			}
			item.SetVal("time", now)

			var values []util.Map
			exists := bucket.Get([]byte(item.StrVal("name")))
			if len(exists) > 0 {
				err := decode(exists, &values)
				if err != nil {
					return err
				}
			}
			values = append(values, item)

			enc, err := encode(values)
			if err != nil {
				return err
			}
			bucket.Put([]byte(item.StrVal("name")), enc)
		}
		return nil
	})
	return err
}

func getAllKeys(res http.ResponseWriter, r *http.Request) {
	var keys []string
	err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		bucket.ForEach(func(k, v []byte) error {
			keys = append(keys, string(k))
			return nil
		})
		return nil
	})

	res.Header().Set("Content-Type", "application/json;charset=utf-8")
	buf, err := json.Marshal(util.Map{"keys": keys})
	if err != nil {
		log.Printf("marshal json error: %v\n", err)
	}
	res.Write(buf)
}

func initDB() (err error) {
	if clear {
		if _, err = os.Stat(dbFile); err == nil {
			os.Remove(dbFile)
		}
	}
	db, err = bolt.Open(dbFile, 0600, nil)
	if err != nil {
		return
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err = tx.CreateBucketIfNotExists([]byte(bucketName))
		return err
	})
	return err
}

func report() (res []util.Map, err error) {
	err = db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		return bucket.ForEach(func(k, v []byte) error {
			item := util.Map{
				"key": string(k),
			}

			var values []util.Map
			err = decode(v, &values)
			if err != nil {
				return err
			}
			item.SetVal("values", values)
			res = append(res, item)
			return nil
		})
	})

	return
}

func handleReportResult(data []util.Map) util.Map {
	if len(data) == 0 {
		return nil
	}
	var max, maxIndex int
	for i, item := range data {
		if max < len(item.AryMapVal("values")) {
			max = len(item.AryMapVal("values"))
			maxIndex = i
		}
	}

	var x []int64
	for _, value := range data[maxIndex].AryMapVal("values") {
		x = append(x, value.IntVal("time"))
	}

	var series []util.Map
	for i := range data {
		var yValue []int64
		for j, value := range data[i].AryMapVal("values") {
			avg := value.IntVal("avg")
			if j == 0 {
				index := indexOf(x, avg)
				for k := 0; k < index; k++ {
					yValue = append(yValue, 0)
				}
			}
			yValue = append(yValue, avg)
		}
		series = append(series, util.Map{
			"type": "line",
			"name": data[i].StrVal("key"),
			"data": yValue,
		})
	}

	return util.Map{
		"series": series,
		"xAxis":  handleXAxis(x),
	}
}

func handleXAxis(x []int64) (res []string) {
	for i := range x {
		res = append(res, formatTime(x[i]))
	}
	return
}

func formatTime(t int64) string {
	tm := time.Unix(0, t*1e6)
	return fmt.Sprintf("%v:%v:%v", tm.Hour(), tm.Minute(), tm.Second())
}

func indexOf(data []int64, target int64) int {
	for i := range data {
		if data[i] == target {
			return i
		}
	}
	return -1
}
func encode(data interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(data)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decode(buf []byte, v interface{}) error {
	dec := gob.NewDecoder(bytes.NewReader(buf))
	err := dec.Decode(v)
	return err
}
