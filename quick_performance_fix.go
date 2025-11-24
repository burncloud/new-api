package main

import (
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
)

// 快速性能优化补丁
// 这个文件提供激进的性能优化，专注于减少数据库查询

// QuotaCacheItem 额度缓存项
type QuotaCacheItem struct {
	Quota     int
	CacheTime time.Time
}

// TokenCacheItem 令牌缓存项
type TokenCacheItem struct {
	Token     *model.Token
	CacheTime time.Time
}

var (
	quotaCache = make(map[int]*QuotaCacheItem)
	tokenCache = make(map[string]*TokenCacheItem)
	cacheMutex sync.RWMutex

	// 异步日志缓冲区
	logBuffer = make([]*model.Log, 0, 1000)
	logMutex  sync.Mutex
	logTicker = time.NewTicker(1 * time.Second) // 1秒批量刷新
)

// InitPerformanceOptimizations 初始化性能优化
func InitPerformanceOptimizations() {
	// 启动异步日志刷新
	gopool.Go(func() {
		for range logTicker.C {
			flushLogs()
		}
	})

	// 启动缓存清理
	gopool.Go(func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			cleanupCache()
		}
	})

	logger.SysLog("激进性能优化已启用 - 缓存TTL: 60s, 日志批量: 1s")
}

// GetUserQuotaOptimized 优化版本的用户额度获取
func GetUserQuotaOptimized(userId int) (int, error) {
	// 先检查内存缓存
	cacheMutex.RLock()
	if item, exists := quotaCache[userId]; exists {
		if time.Since(item.CacheTime) < 60*time.Second {
			cacheMutex.RUnlock()
			return item.Quota, nil
		}
	}
	cacheMutex.RUnlock()

	// 缓存未命中，从数据库获取
	quota, err := model.GetUserQuota(userId, true)
	if err != nil {
		return 0, err
	}

	// 更新缓存
	cacheMutex.Lock()
	quotaCache[userId] = &QuotaCacheItem{
		Quota:     quota,
		CacheTime: time.Now(),
	}
	cacheMutex.Unlock()

	return quota, nil
}

// GetTokenOptimized 优化版本的令牌获取
func GetTokenOptimized(key string) (*model.Token, error) {
	// 先检查内存缓存
	cacheMutex.RLock()
	if item, exists := tokenCache[key]; exists {
		if time.Since(item.CacheTime) < 120*time.Second {
			cacheMutex.RUnlock()
			return item.Token, nil
		}
	}
	cacheMutex.RUnlock()

	// 缓存未命中，从数据库获取
	token, err := model.GetTokenByKey(key, true)
	if err != nil {
		return nil, err
	}

	// 更新缓存
	cacheMutex.Lock()
	tokenCache[key] = &TokenCacheItem{
		Token:     token,
		CacheTime: time.Now(),
	}
	cacheMutex.Unlock()

	return token, nil
}

// AddLogAsync 异步添加日志到缓冲区
func AddLogAsync(log *model.Log) {
	logMutex.Lock()
	defer logMutex.Unlock()

	logBuffer = append(logBuffer, log)

	// 如果缓冲区满了，立即刷新
	if len(logBuffer) >= 1000 {
		flushLogsUnsafe()
	}
}

// flushLogs 刷新日志到数据库
func flushLogs() {
	logMutex.Lock()
	defer logMutex.Unlock()

	flushLogsUnsafe()
}

// flushLogsUnsafe 不加锁的日志刷新
func flushLogsUnsafe() {
	if len(logBuffer) == 0 {
		return
	}

	// 复制缓冲区并清空
	logsToFlush := make([]*model.Log, len(logBuffer))
	copy(logsToFlush, logBuffer)
	logBuffer = logBuffer[:0] // 清空缓冲区

	// 异步批量写入数据库
	gopool.Go(func() {
		if err := model.LOG_DB.Create(&logsToFlush).Error; err != nil {
			logger.LogError(nil, "批量写入日志失败: "+err.Error())
		} else {
			logger.SysLog("批量写入 %d 条日志成功", len(logsToFlush))
		}
	})
}

// cleanupCache 清理过期的缓存
func cleanupCache() {
	now := time.Now()

	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	// 清理额度缓存
	for userId, item := range quotaCache {
		if now.Sub(item.CacheTime) > 120*time.Second {
			delete(quotaCache, userId)
		}
	}

	// 清理令牌缓存
	for key, item := range tokenCache {
		if now.Sub(item.CacheTime) > 300*time.Second {
			delete(tokenCache, key)
		}
	}
}

// InvalidateUserCache 清除用户缓存
func InvalidateUserCache(userId int) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	delete(quotaCache, userId)
}

// InvalidateTokenCache 清除令牌缓存
func InvalidateTokenCache(key string) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	delete(tokenCache, key)
}

// GetCacheStats 获取缓存统计
func GetCacheStats() map[string]int {
	cacheMutex.RLock()
	defer cacheMutex.RUnlock()

	logMutex.Lock()
	logBufferSize := len(logBuffer)
	logMutex.Unlock()

	return map[string]int{
		"quota_cache_size": len(quotaCache),
		"token_cache_size": len(tokenCache),
		"log_buffer_size":  logBufferSize,
	}
}

// 在程序退出时刷新所有日志
func FlushAllLogsOnExit() {
	logger.SysLog("正在刷新所有待写入的日志...")
	flushLogs()
	time.Sleep(2 * time.Second) // 等待异步写入完成
}