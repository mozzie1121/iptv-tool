package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type ShandongChannelProgramListResult struct {
	Data []RawProgram `json:"data"`
}

type RawProgram struct {
	ProgName   string `json:"prog_name"`
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time"`
}

type Program struct {
	ProgramName     string
	BeginTimeFormat string
	EndTimeFormat   string
	StartTime       string
	EndTime         string
}

// 假设这是您获取原始 JSON 数据的函数
func getRawData() ([]byte, error) {
	// 在这里实现获取数据的逻辑，例如 HTTP 请求
	// 下面是一个示例的伪数据
	return []byte(`{"data":[{"prog_name":"节目A","start_time":"01:03:00","end_time":"01:05:00"},{"prog_name":"节目B","start_time":"02:00","end_time":"02:30"}]}`), nil
}

func writeRawDataToFile(filename string, data []byte) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(data)
	if err != nil {
		return err
	}

	return nil
}

func parseShandongChannelDateProgram(rawData []byte) ([]Program, error) {
	var resp ShandongChannelProgramListResult
	if err := json.Unmarshal(rawData, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, errors.New("program list is empty")
	}

	programList := make([]Program, 0, len(resp.Data))
	for _, rawProg := range resp.Data {
		if rawProg.StartTime == "" || rawProg.EndTime == "" {
			return nil, errors.New("StartTime or EndTime is empty")
		}

		// 移除秒部分
		startTimeStr := rawProg.StartTime
		if strings.Contains(startTimeStr, ":") {
			parts := strings.Split(startTimeStr, ":")
			startTimeStr = parts[0] + ":" + parts[1]
		}

		endTimeStr := rawProg.EndTime
		if strings.Contains(endTimeStr, ":") {
			parts := strings.Split(endTimeStr, ":")
			endTimeStr = parts[0] + ":" + parts[1]
		}

		startTime, err := time.Parse("15:04", startTimeStr)
		if err != nil {
			return nil, fmt.Errorf("error parsing StartTime: %v", err)
		}
		endTime, err := time.Parse("15:04", endTimeStr)
		if err != nil {
			return nil, fmt.Errorf("error parsing EndTime: %v", err)
		}

		programList = append(programList, Program{
			ProgramName:     rawProg.ProgName,
			BeginTimeFormat: startTime.Format("20060102150405"),
			EndTimeFormat:   endTime.Format("20060102150405"),
			StartTime:       startTime.Format("15:04"),
			EndTime:         endTime.Format("15:04"),
		})
	}

	return programList, nil
}

func getShandongChannelDateProgram() ([]Program, error) {
	rawData, err := getRawData()
	if err != nil {
		return nil, err
	}

	// 将未解析的原始数据写入到文件
	err = writeRawDataToFile("raw_data.json", rawData)
	if err != nil {
		return nil, fmt.Errorf("could not write raw data to file: %v", err)
	}

	// 继续解析 JSON 数据
	return parseShandongChannelDateProgram(rawData)
}

func main() {
	programs, err := getShandongChannelDateProgram()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	// 打印解析后的节目单
	for _, program := range programs {
		fmt.Printf("Program: %s, Start: %s, End: %s\n", program.ProgramName, program.StartTime, program.EndTime)
	}
}
