---
name: clawhub-tools
description: ClawHub/OpenClaw 生态接入：搜索与安装 OpenClaw 插件、管理插件生命周期、给插件通道收发消息。装 ClawHub 上的东西之前先读这个。
version: 1.0.0
---

# ClawHub 适配器

把 **ClawHub / OpenClaw** 生态的插件和技能装进本机。它是个**兼容层**：
外部包的格式不改成我们的，由它在中间转译。

## 什么时候用它

- 用户说"装个 ClawHub 上的 xxx""OpenClaw 有个插件""从 clawhub 装"
- 需要一个本机没有的能力，而它可能已存在于 OpenClaw 生态
- 管理已装的兼容层插件（列、查、重载、卸载）

**不要**用它装 HologramAgent 自己的插件（那是 `plugin_install` / `pluginmgr`，
`plugin.bin` 格式）。两者是不同的东西，混用会失败。

## 工具

| 工具 | 用途 |
|---|---|
| `clawhubadapter_search` | 搜 ClawHub 市场，返回名字/描述/安装命令 |
| `clawhubadapter_plugin_install` | 安装。`package=npm:<包>` 从 npm，`package=clawhub:<名>` 从市场 |
| `clawhubadapter_plugin_list` | 列出已装的兼容层插件及其工具 |
| `clawhubadapter_plugin_info` | 查单个插件：类型（OC/sidecar/SKILL）、工具、通道、运行状态 |
| `clawhubadapter_plugin_reload` | 代码或配置变了之后重载 |
| `clawhubadapter_plugin_uninstall` | 停进程、删目录、清理注册 |
| `clawhubadapter_channel_list` | 列出兼容层插件注册的消息通道与状态 |
| `clawhubadapter_channel_send` | 往插件通道注入一条消息 |
| `clawhubadapter_channel_start` / `_stop` | 起停通道账号（心跳/收消息轮询） |

## 装之前先搜

**不要凭想象拼包名**。先 `clawhubadapter_search` 看真实的名字，
再用返回里的安装命令 —— 名字拼错不会报错，只会装到别的东西。

## 装完可能没有工具，这是正常的

兼容层把包分四类，只有前两类会产出工具：

| 包形态 | 装完的表现 |
|---|---|
| OpenClaw sidecar（有 `main.js` / `main.py`） | 起来 sidecar，工具以 `<包名>_<工具名>` 出现 |
| OpenClaw 插件（有 `openclaw.plugin.json`） | 交给内部 manager，工具同样出现 |
| 纯 SKILL（只有 `SKILL.md`） | **不产出工具**。它是一份说明文档，由 skillmgr 索引 |
| Python CLI 型（`main.py` 但不是 RPC） | 降级为纯 SKILL，日志会写 `treating as CLI-type skill` |

纯 SKILL 那类装了之后的正确用法是：读它的 SKILL 文档，按里面的命令
用 `cmd_run` 跑它 —— 它本来就不是"工具"，而是一套操作说明。

## 排查

装完工具没出现，按顺序查：

1. `clawhubadapter_plugin_list` —— 它在不在？类型是什么？
2. `clawhubadapter_plugin_info` —— 工具列表空的话，是上面哪一类？
3. 日志里搜包名，看有没有 `sidecar ... not ready` 或 `launch` 失败
4. Python 型报 `ModuleNotFoundError` ⇒ 解释器里缺依赖。
   配 `python_bin` 指向带依赖的 venv，**不要**往系统 Python 里 pip install
   （PEP 668 禁止，会破坏 apt 的包一致性）

## 边界

- 安装会从网络下载并解压执行外部代码。**装之前先跟用户确认包名**
- `plugin_uninstall` 会删目录，不可撤销
- 通道类插件（微信/钉钉等）的收发依赖它自己的轮询，`channel_send` 只是投递，
  不代表对方已读
