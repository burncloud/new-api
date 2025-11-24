package model

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// AsyncLogBuffer 异步日志缓冲区
type AsyncLogBuffer struct {
	buffer    []*Log
	mutex     sync.RWMutex
	maxSize   int
	flushTick time.Duration
	ticker    *time.Ticker
}

var logBuffer *AsyncLogBuffer

// InitAsyncLogBuffer 初始化异步日志缓冲区
func InitAsyncLogBuffer() {
	logBuffer = &AsyncLogBuffer{
		buffer:    make([]*Log, 0, 500),
		maxSize:   500,      // 缓冲区大小
		flushTick: 2 * time.Second, // 2秒刷新一次，大幅减少延迟
	}

	// 启动异步刷新协程
	logBuffer.startFlushRoutine()

	logger.SysLog("异步日志缓冲区已初始化，缓冲区大小: %d, 刷新间隔: %v", logBuffer.maxSize, logBuffer.flushTick)
}

// startFlushRoutine 启动刷新协程
func (lb *AsyncLogBuffer) startFlushRoutine() {
	lb.ticker = time.NewTicker(lb.flushTick)

	gopool.Go(func() {
		for range lb.ticker.C {
			lb.flush()
		}
	})
}

// addLog 添加日志到缓冲区
func (lb *AsyncLogBuffer) addLog(log *Log) {
	lb.mutex.Lock()
	defer lb.mutex.Unlock()

	lb.buffer = append(lb.buffer, log)

	// 如果缓冲区满了，立即刷新
	if len(lb.buffer) >= lb.maxSize {
		lb.flushUnsafe()
	}
}

// flush 刷新缓冲区到数据库
func (lb *AsyncLogBuffer) flush() {
	lb.mutex.Lock()
	defer lb.mutex.Unlock()

	lb.flushUnsafe()
}

// flushUnsafe 不加锁的刷新方法（调用者必须持有锁）
func (lb *AsyncLogBuffer) flushUnsafe() {
	if len(lb.buffer) == 0 {
		return
	}

	// 复制缓冲区并清空
	logsToFlush := make([]*Log, len(lb.buffer))
	copy(logsToFlush, lb.buffer)
	lb.buffer = lb.buffer[:0] // 清空缓冲区

	// 异步批量写入数据库
	gopool.Go(func() {
		if err := LOG_DB.CreateInBatches(logsToFlush, 100).Error; err != nil {
			logger.LogError(nil, "批量写入日志失败: "+err.Error())
		} else {
			logger.SysLog("批量写入 %d 条日志成功", len(logsToFlush))
		}
	})
}

// RecordConsumeLogAsync 异步记录消费日志（优化版本）
func RecordConsumeLogAsync(c *gin.Context, userId int, params RecordConsumeLogParams) {
	if !common.LogConsumeEnabled {
		return
	}

	// 异步记录日志信息，不阻塞主流程
	gopool.Go(func() {
		logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))

		username := c.GetString("username")
		otherStr := common.MapToJsonStr(params.Other)

		// 异步获取用户设置，避免阻塞
		var needRecordIp bool
		if settingMap, err := GetUserSetting(userId, false); err == nil {
			if settingMap.RecordIpLog {
				needRecordIp = true
			}
		}

		log := &Log{
			UserId:           userId,
			Username:         username,
			CreatedAt:        common.GetTimestamp(),
			Type:             LogTypeConsume,
			Content:          params.Content,
			PromptTokens:     params.PromptTokens,
			CompletionTokens: params.CompletionTokens,
			TokenName:        params.TokenName,
			ModelName:        params.ModelName,
			Quota:            params.Quota,
			QuotaDelta:       params.QuotaDelta,
			GroupId:          params.GroupId,
			TokenId:          params.TokenId,
			UseTimeSeconds:   params.UseTimeSeconds,
			IsStream:         params.IsStream,
			Group:            params.Group,
			Other:            params.Other,
		}

		// IP记录处理
		if needRecordIp {
			log.IpAddress = c.ClientIP()
		}

		// 添加到异步缓冲区而不是直接写入数据库
		if logBuffer != nil {
			logBuffer.addLog(log)
		} else {
			// 如果异步缓冲区未初始化，回退到同步写入
			LOG_DB.Create(log)
		}

		// 数据导出保持异步
		if common.DataExportEnabled {
			gopool.Go(func() {
				LogQuotaData(userId, username, params.ModelName, params.Quota, common.GetTimestamp(), params.PromptTokens+params.CompletionTokens)
			})
		}
	})
}

// FlushAllLogs 手动刷新所有待写入的日志（用于优雅关闭）
func FlushAllLogs() {
	if logBuffer != nil {
		logBuffer.flush()
		// 等待最后一次刷新完成
		time.Sleep(100 * time.Millisecond)
	}
}

// GetLogBufferStatus 获取日志缓冲区状态（用于监控）
func GetLogBufferStatus() (int, int, time.Duration) {
	if logBuffer == nil {
		return 0, 0, 0
	}

	logBuffer.mutex.RLock()
	defer logBuffer.mutex.RUnlock()

	return len(logBuffer.buffer), logBuffer.maxSize, logBuffer.flushTick
}