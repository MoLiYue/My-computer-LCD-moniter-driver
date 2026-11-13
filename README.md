# Miku Screen Hub

统一控制爱国者 1920×462 机箱长条屏和华硕 TX GAMING LC III 320×320 水冷屏。当前版本 **2.0.0**，后台由 Go 实现，内置初音主题直接绘制，自定义网页才启用 WebView2。

长条屏显示时间、GPU、内存与显存占用；水冷屏显示 CPU 占用和温度。两屏独立渲染、传输和重连。水冷屏连接时先确认小黑图应答，传输超时后先重建并验证会话，再重试画面。正常关机、重启或注销时，长条屏进入休眠，水冷屏显示全黑画面；尚未确认水冷屏关闭背光的硬件指令。

## 从源码运行

需要 Windows x64 和 Go 1.25+（仅构建时使用）。运行内置主题需要微软雅黑粗体、两个随仓库提供的 PowerShell 传感器脚本，以及读取 CPU 温度所需的 ASUS asComSvc / atkexCom.axdata。程序不控制风扇或水泵。

在仓库目录执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\build-go.ps1 -Test
.\MikuScreenHub.exe --start --open
```

构建脚本生成 `MikuScreenHub.exe`，控制台和内置主题素材嵌入 EXE。运行后打开本机控制台 http://127.0.0.1:19330/ 。原有 `data/config.json` 可直接沿用。测试不向 USB 屏写入数据，也不会让 Windows 关机；可用 `MikuScreenHub.exe --probe-hid asus` 或 `--probe-hid aigo` 只读枚举设备。

Go 版直接使用 Windows HID，JPEG 编码及水冷画面旋转在进程内完成，不依赖 Node.js、hid.node、FFmpeg 或单独的关机监听器。内置主题无需启动 Chromium。选择自定义网址时，先运行 `build-webview.ps1` 构建可选助手，并使用系统已安装的 [WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution)。仅使用内置主题时可跳过这一步。

旧 Node 版源码及构建脚本保留作回退。先在控制台退出 Go 程序，再执行 `powershell -NoProfile -ExecutionPolicy Bypass -File .\autostart.ps1 -LegacyNode` 可临时运行旧版。现有「Miku Screen Hub」登录任务默认启动 Go EXE，无需重新注册。

## 源码结构

- `main.go`、`config_go.go`：本机控制台接口、配置与进程生命周期。
- `display_go.go`、`protocol.go`、`hid_windows.go`：独立传输、Windows HID 与故障恢复。
- `render.go`、`webview_go.go`、`WebViewRenderer.cs`：原生绘图和自定义网页截图。
- `public/`：控制台、屏幕界面和素材。
- `telemetry_windows.go`、`*-reader.ps1`：Windows CPU、内存接口及 GPU、显存、温度采集。
- `session_windows.go`：进程内监听 Windows 会话结束。
- `hub_test.go`：Go 版协议、控制接口、原生渲染及应答恢复测试。
- `autostart.ps1`、`install-autostart.ps1`：当前电脑的登录自启配置；安装脚本针对用户 11274，其他电脑需修改。

完整使用说明和实际测试范围见 [使用说明](使用说明.md) 与 [验证记录](验证记录.md)。图片来源见 [长条屏图片来源](长条屏图片来源.md) 和 [水冷屏图片来源](水冷屏图片来源.md)。素材版权归原作者，第三方许可证保留在 `vendor/`。

## Git 保存范围

仓库直接位于正在使用的程序目录，初始化使用 `main` 分支。保存源码、测试、构建脚本、网页素材、说明和必要原生依赖；忽略 `data/`、浏览器登录状态、日志、开机任务备份及生成的监听器 EXE。当前配置不提交，默认显示设置由 `app.js` 定义。

日常修改后可用 `git status` 查看变化，再提交需要保存的文件。远程仓库为 [MoLiYue/My-computer-LCD-moniter-driver](https://github.com/MoLiYue/My-computer-LCD-moniter-driver)，`main` 跟踪 `origin/main`；本地新提交需执行 `git push` 才会上传。
