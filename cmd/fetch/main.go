package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/schollz/progressbar/v3"
)

func usage() {
	fmt.Println("Usage: jabgo <url>")
}

type TsFile struct {
	name string
	path string
	url  string
}

func TsFileExisits(filepath string) bool {
	_, err := os.Stat(filepath)

	// 文件是否存在
	return err == nil
}

func M3u8CountTsFileNumber(lines []string) int64 {
	count := int64(0)

	for _, line := range lines {
		if !strings.Contains(line, "#") && strings.Contains(line, ".ts") {
			count += 1
		}
	}

	return count
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	startTime := time.Now()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var bar *progressbar.ProgressBar

	url := os.Args[1]

	downloadPath := "download"

	os.Mkdir(downloadPath, 0777)

	split := strings.Split(url, "/")

	dirname := split[len(split)-2]

	dirname = filepath.Join(downloadPath, dirname)

	os.Mkdir(dirname, 0777)

	mp4Path := filepath.Join(dirname, "mp4")
	os.Mkdir(mp4Path, 0777)

	tsPath := filepath.Join(dirname, "ts")
	os.Mkdir(tsPath, 0777)

	tsLink := ""

	// foundCh := make(chan bool)
	var foundwg sync.WaitGroup
	foundwg.Add(1)

	tsFileCh := make(chan TsFile, 1000)

	downloadFailFilePath := filepath.Join(dirname, "fail.url.txt")
	downloadFileHandler, err := os.OpenFile(downloadFailFilePath, os.O_CREATE|os.O_APPEND, 0777)

	// 下載器
	for range 15 {
		go func() {

			if err != nil {
				log.Fatal(err.Error())
			}

			for ts := range tsFileCh {
				if TsFileExisits(ts.path) {
					log.Printf("%s 文件已存在", ts.name)
					bar.Add(1)
					wg.Done()
					continue
				}

				resp, err := http.Get(ts.url)
				if err != nil {
					log.Println(ts.url + ": 下載失敗")

					mu.Lock()
					downloadFileHandler.WriteString(ts.url + "\n")
					mu.Unlock()

					wg.Done()
					continue
				}

				data, err := io.ReadAll(resp.Body)

				if err != nil {
					log.Println(ts.path + ": 寫入失敗")
					wg.Done()
					continue
				}

				os.WriteFile(ts.path, data, 0777)

				log.Println(ts.name + ": 下載成功")

				bar.Add(1)

				resp.Body.Close()

				wg.Done()
			}
		}()
	}

	title := ""

	go func() {
		ctx, cancel := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath("C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe"),
			chromedp.Flag("headless", false), // 启用有头模式
		)...)
		defer cancel()

		// 创建一个上下文
		ctx, cancel = chromedp.NewContext(ctx)
		defer cancel()

		chromedp.ListenTarget(ctx, func(ev interface{}) {
			if reqEvent, ok := ev.(*network.EventRequestWillBeSent); ok {
				url := reqEvent.Request.URL

				if strings.Contains(url, ".m3u8") {
					fmt.Println("m3u8文件找到了！")
					tsLink = url
					foundwg.Done()
					cancel()
					return
				}
			}
		})

		// 执行浏览器自动化任务
		_ = chromedp.Run(ctx,
			chromedp.Navigate(url),
			chromedp.WaitReady("html"),
			chromedp.Title(&title),
			chromedp.ActionFunc(func(ctx context.Context) error {
				foundwg.Wait()
				return nil
			}),
		)
		// if err != nil {
		// 	log.Fatal(err.Error())
		// }
	}()

	foundwg.Wait()

	fmt.Println("開始下載m3u8文件")

	resp, err := http.Get(tsLink)
	if err != nil {
		log.Fatal("tsLink GET請求失敗")
	}

	defer resp.Body.Close()

	fmt.Println("tsLink: ", tsLink)

	URIs := strings.Split(tsLink, "/")
	lastURIIndex := len(URIs) - 2
	m3u8Filename := URIs[lastURIIndex]
	m3u8Filename += ".m3u8"

	m3u8FileData, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err.Error())
	}

	m3u8FilePath := filepath.Join(tsPath, m3u8Filename)
	fmt.Println("m3u8FilePath: ", m3u8FilePath)

	err = os.WriteFile(m3u8FilePath, m3u8FileData, 0777)
	if err != nil {
		log.Fatal("m3u8文件寫入失敗")
	}

	content := string(m3u8FileData)

	lines := strings.Split(content, "\n")

	n := M3u8CountTsFileNumber(lines)

	bar = progressbar.Default(n)

	for _, line := range lines {
		if strings.Contains(line, "URI=") {
			// 正则表达式匹配 ts 文件名
			re := regexp.MustCompile(`URI="([^"]+)"`)
			match := re.FindStringSubmatch(line)
			decryptoKEYFilename := ""

			if len(match) > 1 {
				decryptoKEYFilename = match[1]
				URIs[len(URIs)-1] = decryptoKEYFilename
				tsFileUrl := strings.Join(URIs, "/")
				tsFilePath := filepath.Join(tsPath, decryptoKEYFilename)

				wg.Add(1)

				tsFileCh <- TsFile{name: decryptoKEYFilename, path: tsFilePath, url: tsFileUrl}
				fmt.Println("解密的URI: ", decryptoKEYFilename)
			} else {
				log.Fatal("找到的m3u8文件格式有誤")
			}
		}

		if !strings.Contains(line, "#") && strings.Contains(line, ".ts") {
			tsFilename := line
			URIs[len(URIs)-1] = tsFilename
			tsFileUrl := strings.Join(URIs, "/")
			// fmt.Println("URL: ", tsFileUrl)
			tsFilePath := filepath.Join(tsPath, tsFilename)

			wg.Add(1)

			tsFileCh <- TsFile{name: tsFilename, path: tsFilePath, url: tsFileUrl}
		}
	}

	// 等待所有下載ts文件完成。
	wg.Wait()
	close(tsFileCh)

	// 下載完成後，將ts文件合并mp4文件。
	mp4FilePath := filepath.Join(mp4Path, "video.mp4")

	command := fmt.Sprint("ffmpeg", " -protocol_whitelist \"file,http,crypto,tcp\" -i ", m3u8FilePath, " -c copy ", mp4FilePath)

	fmt.Println(command)

	cmd := exec.Command("ffmpeg", "-protocol_whitelist", "file,http,crypto,tcp", "-i", m3u8FilePath, "-c", "copy", mp4FilePath)
	output, err := cmd.Output()
	if err != nil {
		log.Fatal(output)
	}

	fmt.Printf("[+]\tTotal Time: %v\t[+]\n", time.Since(startTime))
	fmt.Println("[+]\tDownload Successful\t[+]")
}
