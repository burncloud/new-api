#!/bin/bash

echo "🚀 New-API 性能监控脚本"
echo "=================================="

# 检查Docker容器状态
echo "📊 容器资源使用情况："
sudo docker stats --no-stream --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.BlockIO}}" new-api postgres redis

echo -e "\n🔍 数据库连接数："
sudo docker exec postgres psql -U root -d new-api -c "SELECT count(*) as active_connections FROM pg_stat_activity WHERE state = 'active';" 2>/dev/null || echo "无法连接到PostgreSQL"

echo -e "\n📈 Redis连接数："
sudo docker exec redis redis-cli info clients | grep connected_clients 2>/dev/null || echo "无法连接到Redis"

echo -e "\n⏱️ 最新API响应时间（最后10个请求）："
sudo docker logs new-api --tail 50 | grep "GIN.*POST.*v1/chat/completions" | tail -10 | awk '{print $8, $9, $10, $11}' || echo "暂无API请求日志"

echo -e "\n🔥 热点查询（慢查询）："
sudo docker exec postgres psql -U root -d new-api -c "SELECT query, mean_time, calls FROM pg_stat_statements ORDER BY mean_time DESC LIMIT 5;" 2>/dev/null || echo "需要启用pg_stat_statements扩展"

echo -e "\n📝 预扣费操作频率："
sudo docker logs new-api --tail 100 | grep "预扣费后补扣费" | wc -l

echo -e "\n=================================="
echo "🎯 性能优化建议："
echo "1. 如果 CPU 使用率 > 80%，考虑增加 CPU 资源"
echo "2. 如果内存使用率 > 85%，考虑增加内存或优化缓存"
echo "3. 如果数据库连接数接近上限，调整 SQL_MAX_OPEN_CONNS"
echo "4. 如果 Redis 连接数接近上限，调整 REDIS_POOL_SIZE"
echo "5. 监控预扣费操作频率，过高可能需要优化扣费逻辑"