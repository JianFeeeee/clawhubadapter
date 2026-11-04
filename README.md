# clawhubadapter

把 **ClawHub / OpenClaw 生态**的插件与技能接入 HologramAgent。

外部插件形态（`plugin.bin` + `plugin.json`），与内核解耦；源码来自内核仓
`internal/plugins/clawhubadapter/`（2026-11-04 迁出）。

## 它做什么

扫描 ClawHub 的 skills 目录，按包形态分别处理：

| 包形态 | 判定标志 | 处理 |
|---|---|---|
| OpenClaw sidecar（JS） | `main.js` | 起 Node 子进程，JSON-RPC 注册工具/通道/Provider |
| OpenClaw sidecar（Python） | `main.py` | 同上，解释器可配（`python_bin`） |
| OpenClaw 插件 | `openclaw.plugin.json` 或 package.json 带 `openclaw` | 交给内置 manager 进程 |
| **纯 SKILL** | 有 `SKILL.md` / `skill.json` | 留在 skills 目录，由 skillmgr 扫描注册 |

## 配置项

| 键 | 说明 |
|---|---|
| `skills_dir` | OpenClaw 技能加载目录。留空 → `<data_dir>/skills` |
| `simulator_dir` | 模拟器工作目录。留空 → `<skills_dir>/.simulator` |
| `python_bin` | 跑 Python skill 的解释器绝对路径。留空 → `<skill>/.venv/bin/python` → 系统 `python3` |
| `skill_workdir` | skill 子进程工作目录。留空 → skill 自身目录 |

**`python_bin` 为什么要显式配**：不同部署的 Python 布局完全不同（发行版 /
pyenv / uv / conda / venv），"猜"没有正确解。实测事故：`bilibili-all-in-one`
声明依赖 `httpx`，系统 Python 没装 ⇒ 启动即 `ModuleNotFoundError`。
配了但文件不存在会**报错**，不会静默退回系统解释器。

## 工具

`clawhubadapter_` 前缀，共 10 个：

```
plugin_install  plugin_uninstall  plugin_list  plugin_info  plugin_reload
search
channel_list  channel_send  channel_start  channel_stop
```

## 外部插件的能力边界（迁移时踩过的）

这个工程能编译、能跑，但有**三条硬约束**，改代码前先读：

### 1. 入口函数必须叫 `NewPluginFactory`

`hmapdev build` 生成的 proc 运行时按**名字**调用。写成 `NewPlugin` 会报
`undefined: NewPluginFactory`（错误指向生成代码，很难第一眼反应过来）。

判据：`TestEntryPointIsNewPluginFactory`

### 2. 不能用 `Events().Publish`

内置版用它在发现纯 SKILL 时发 `skill_detected` 事件移交 skillmgr。
**外部插件拿不到**，三层证据：

- `internal/plugin/proc/capability_test.go` 把 `events.publish` 列进
  `forbiddenPrefixes` —— 内核刻意不提供，且有测试守着不许加回去
- proc 协议表只有 `events.subscribe` / `events.unsubscribe`
- 外部插件模板不注入 subscriber ⇒ `Events()` 恒 `nil`

`EventSubscriber` 接口本身没有 `Publish` 方法，所以这是**编译期**就拦住
（实测变异：加回去 ⇒ `s.Events().Publish undefined`）。

现在的做法：条目留在 skills 目录，由 skillmgr 的**启动全扫**（`scanExisting`，
幂等）或 `skill_load` 工具热加载拾取。

> 顺带一条：**插件自带的 skill** 是另一条路 —— 放在工程根 `skill/` 目录，
> `hmapdev build` 会打进包，内核/pluginmgr 在安装与加载时自动扫
> `<插件目录>/skill/` 并移交。这条路不需要任何事件。

判据：`TestNoEventPublish`

### 3. 不能 import 内核 `internal/*`

外部插件用公版 SDK（`github.com/JianFeeeee/hologramagentsdk`），
内核仓的 `internal/*` 对它不存在。迁移时有两个点因此改掉：

| 内置 | 外部替代 |
|---|---|
| `internal/safeclose.Safe(&once, ch)` | 本工程的 `safeClose[T any](once, ch)` |
| `InjectInputSync(src, ch, "text", payload) *OutputEvent` | `InjectInputSync(src, ch, text) string` |

判据：`TestNoKernelInternalImports`、`TestSafeCloseCoversAllChannelKinds`

## 构建与安装

```bash
hmapdev build                        # → dist/clawhubadapter_bundle.hmap（ZIP）
unzip -o dist/clawhubadapter_bundle.hmap 'plugin.bin.linux.amd64'
mkdir -p <plugin.dir>/clawhubadapter
cp plugin.json plugin.bin.linux.amd64 <plugin.dir>/clawhubadapter/
mv <plugin.dir>/clawhubadapter/plugin.bin.linux.amd64 <plugin.dir>/clawhubadapter/plugin.bin
chmod +x <plugin.dir>/clawhubadapter/plugin.bin
systemctl restart homed
```

**★ 不要往 stdout 打印**：子进程的 stdout 是 JSON-RPC 协议通道，
stderr 才是日志。用 `log.Printf`，不要 `fmt.Printf`。

## 路径安全（2026-11-04 修复，迁移时一并带过来）

三条入口会把**外部字符串**拼进路径，其中 `name` 来自**模型**（工具参数）：

| 缺陷 | 后果 | 修法 |
|---|---|---|
| `uninstall name=".."` | `Join(skillsDir,"..")` == data_dir ⇒ `RemoveAll` 清空整个数据目录 | `resolveUnderRoot` |
| ZIP Slip（归档含 `../../x`） | 解压写任意路径（且下载无校验和） | `safeArchiveTarget` |
| `p.manager` 解锁后裸读 | 与 `Stop()` 写 `= nil` 竞争 | 补锁 |

判据：`TestUninstallRejectsTraversal`、`TestExtractZipRejectsTraversal`、
`TestExtractTarRejectsTraversal` 等 8 条，均经变异验证。

## 未随迁的部分

- `openclaw_cli.js` 包装脚本（`simulator/bin/openclaw`）：仍在 `simulator/` 下，
  `go:embed` 引用，已随包。
- 生产部署路径与单元测试数据（`testdata/`）已随迁。
