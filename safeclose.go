package main

// safeClose 是全仓 close 纪律的外部插件版本。
//
// ## 为什么要自己写一份
//
// 内核仓有 internal/safeclose（`Safe(&once, ch)`），但那是 internal 包，
// 外部插件 import 不到。语义必须一模一样，否则这里会成为纪律的缺口：
//
// 「只要调 close 就需要前置检查，这是为了避免未来更新的意外修改。」
//
// 本仓出过真实崩溃（真机 SIGTERM）：
//
//	panic: close of closed channel
//	main.startHeartbeat.func3()  bootstrap.go:259
//
// 根因是同一个 channel 有两个调用点。今天只在退出时调一次的代码，
// 明天加一条错误/超时/重连路径就变成两次 —— 而那次改动看起来与 close 无关，
// 代码评审不会注意。所以不能靠"逐处判断当前是否安全"，必须统一入口。
//
// ## 为什么是 sync.Once 而不是 select 探测
//
// 常见错法是：
//
//	if _, ok := <-ch; !ok { return }   // ❌ 会**消费**一条消息或永久阻塞
//
// sync.Once 是唯一的无副作用方案：第一次执行体，之后空转。
// 代价是 Once 不可重入 —— 所以每个**逻辑上独立**的生命周期要独占一个 Once
// （本插件里 stopCh / readerReady / 每个 pendingResp 各一个）。

import "sync"

// safeClose 在 once 保护下关闭 ch。
//
// 用泛型而不是固定 chan struct{}：本插件里有三种 channel
// （stopCh chan struct{}、readerReady chan struct{}、pendingResp.ch chan []byte）。
// 写三个重载容易漏，且将来新增一种类型时编译器不会提醒你。
//
// once 必须由调用方持有：同一个 channel 的所有 close 路径要传**同一个** Once。
// 传错（各路径各传一个）等于没有保护。
func safeClose[T any](once *sync.Once, ch chan T) {
	once.Do(func() { close(ch) })
}
