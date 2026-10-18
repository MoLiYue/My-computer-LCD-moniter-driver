# Miku Screen Hub

统一控制爱国者 1920×462 机箱长条屏和华硕 TX GAMING LC III 320×320 水冷屏，支持网页画面、硬件数据和初音主题。当前版本 1.2.0。

长条屏显示时间、GPU、内存与显存占用；水冷屏显示 CPU 占用和温度。两屏独立渲染、传输和重连。正常关机、重启或注销时，长条屏进入休眠，水冷屏显示全黑画面；尚未确认水冷屏关闭背光的硬件指令。

## 从源码运行

需要 Windows x64、Node.js 22+、Chrome 或 Edge、FFmpeg，以及 .NET Framework 4.x。CPU 温度使用本机已有 ASUS asComSvc / atkexCom.axdata 组件；程序不控制风扇或水泵。

在仓库目录执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\build.ps1
node test.js
node app.js --start --open
```

构建脚本生成 `ShutdownWatcher.exe`；随后打开本机控制台 http://127.0.0.1:19330/ 。测试不会启动屏幕传输，也不会让 Windows 关机。首次运行会自动建立 `data/` 并使用内置默认配置，无需复制现有电脑的配置文件。

`vendor/hid.node` 是运行所需的 Windows x64 原生 HID 依赖，随对应许可证一并保存。项目使用 Node.js 内置模块，不需要运行 npm install。可用 `SCREEN_BROWSER` 指定浏览器、`ASUS_FFMPEG` 指定 FFmpeg。

## 源码结构

- `app.js`、`config.js`：本机控制接口、配置和子进程管理。
- `display-worker.js`、`protocols/`：设备协议、独立传输与故障恢复。
- `renderer.js`、`public/`：网页截图、控制台和屏幕界面及素材。
- `telemetry.js`、`*-reader.ps1`：CPU、内存、GPU、显存和温度采集。
- `session-watcher.js`、`ShutdownWatcher.cs`：Windows 会话结束监听与熄屏。
- `*.test.js`、`test.js`：控制接口、生命周期和会话通知验证。
- `autostart.ps1`、`install-autostart.ps1`：当前电脑的登录自启配置；安装脚本针对用户 11274，其他电脑需修改。

完整使用说明和实际测试范围见 [使用说明](使用说明.md) 与 [验证记录](验证记录.md)。图片来源见 [长条屏图片来源](长条屏图片来源.md) 和 [水冷屏图片来源](水冷屏图片来源.md)。素材版权归原作者，第三方许可证保留在 `vendor/`。

## Git 保存范围

仓库直接位于正在使用的程序目录，初始化使用 `main` 分支。保存源码、测试、构建脚本、网页素材、说明和必要原生依赖；忽略 `data/`、浏览器登录状态、日志、开机任务备份及生成的监听器 EXE。当前配置不提交，默认显示设置由 `app.js` 定义。

日常修改后可用 `git status` 查看变化，再提交需要保存的文件。仓库当前只保存在本机，尚未配置远程地址或上传 GitHub。
