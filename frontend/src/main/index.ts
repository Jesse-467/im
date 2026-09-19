import { app, BrowserWindow, ipcMain, shell } from 'electron'
import { join } from 'node:path'
import { URL } from 'node:url'

/**
 * 微语 IM 桌面客户端主进程。
 *
 * 职责保持最小：
 *   - 创建无边框窗口（QQ 风格的自绘标题栏由渲染层实现）
 *   - 提供窗口控制与 HTTP 转发两个 IPC 通道
 *
 * HTTP 请求经由主进程转发而不是让渲染层直连后端：
 * 后端（Gin）没有开 CORS，渲染层无论在 dev（http://localhost:5173）
 * 还是 prod（file://）下直连都会被浏览器同源策略拦截；
 * 而主进程的 fetch 属于 Node 环境，不受 CORS 约束。
 */

const APP_NAME = '微语'

/**
 * 多开实例分身。
 *
 * 主进程没有 requestSingleInstanceLock，本就允许多开；但多个实例共享
 * 同一 userData 目录 → localStorage 共享 → 登录态/设置互相覆盖，
 * 无法测试多账号或多设备场景。
 *
 * 通过环境变量 IM_INSTANCE 或命令行参数 --im-instance=<N> 指定实例号，
 * 各实例使用独立的 userData 子目录，存储完全隔离：
 *   IM_INSTANCE=2 pnpm dev          （类 Unix shell）
 *   $env:IM_INSTANCE=2; pnpm dev    （PowerShell）
 *   微语.exe --im-instance=2        （打包后的 exe）
 *
 * 必须在 app ready 之前调用（setPath 对 userData 生效窗口）。
 */
function applyInstanceProfile(): void {
  const argvFlag = process.argv.find((a) => a.startsWith('--im-instance='))
  const instance =
    process.env.IM_INSTANCE?.trim() || argvFlag?.split('=')[1]?.trim() || ''
  if (!instance || !/^[\w.-]+$/.test(instance)) return

  const base = app.getPath('userData')
  const dir = join(base, `instance-${instance}`)
  app.setPath('userData', dir)
  console.log(`[im] 实例分身 #${instance}：userData = ${dir}`)
}

function createWindow(): void {
  const win = new BrowserWindow({
    width: 1280,
    height: 840,
    minWidth: 1080,
    minHeight: 720,
    show: false,
    frame: false,
    titleBarStyle: 'hidden',
    backgroundColor: '#e9eef6',
    title: APP_NAME,
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      sandbox: false,
      contextIsolation: true,
      webSecurity: true,
      spellcheck: false
    }
  })

  win.on('ready-to-show', () => {
    win.show()
  })

  // 最大化状态变化同步给渲染层，用于切换「最大化/还原」图标
  const sendMaximized = (): void => {
    if (!win.isDestroyed()) {
      win.webContents.send('window:maximized-changed', win.isMaximized())
    }
  }
  win.on('maximize', sendMaximized)
  win.on('unmaximize', sendMaximized)

  // 页面内链接交给系统浏览器，不在应用内导航
  win.webContents.setWindowOpenHandler(({ url }) => {
    void shell.openExternal(url)
    return { action: 'deny' }
  })

  if (process.env.ELECTRON_RENDERER_URL) {
    void win.loadURL(process.env.ELECTRON_RENDERER_URL)
  } else {
    void win.loadFile(join(__dirname, '../renderer/index.html'))
  }
}

function registerIpc(): void {
  // ── 窗口控制 ──
  ipcMain.on('window:minimize', (event) => {
    BrowserWindow.fromWebContents(event.sender)?.minimize()
  })
  ipcMain.on('window:maximize', (event) => {
    const win = BrowserWindow.fromWebContents(event.sender)
    if (!win) return
    if (win.isMaximized()) {
      win.unmaximize()
    } else {
      win.maximize()
    }
  })
  ipcMain.on('window:close', (event) => {
    BrowserWindow.fromWebContents(event.sender)?.close()
  })

  // ── HTTP 转发 ──
  ipcMain.handle(
    'http:fetch',
    async (
      _event,
      opts: {
        url: string
        method?: 'GET' | 'POST'
        headers?: Record<string, string>
        body?: string
        timeoutMs?: number
      }
    ) => {
      const controller = new AbortController()
      const timer = setTimeout(() => controller.abort(), opts.timeoutMs ?? 10_000)
      try {
        const res = await fetch(opts.url, {
          method: opts.method ?? 'POST',
          headers: opts.headers,
          body: opts.body,
          signal: controller.signal
        })
        const body = await res.text()
        return { ok: res.ok, status: res.status, body }
      } catch (err) {
        return {
          ok: false,
          status: 0,
          body: '',
          error: err instanceof Error ? err.message : String(err)
        }
      } finally {
        clearTimeout(timer)
      }
    }
  )
}

applyInstanceProfile()

void app.whenReady().then(() => {
  registerIpc()
  createWindow()

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})
