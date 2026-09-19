import type { CapacitorConfig } from '@capacitor/cli'

/**
 * Capacitor 安卓壳配置。
 *
 * 复用 electron-vite 构建出的渲染层产物（out/renderer，纯静态 Web 资源），
 * 打包进安卓 WebView 运行：
 *   pnpm build && npx cap sync android && cd android && ./gradlew assembleDebug
 *
 * - androidScheme https：WebView 以 https://localhost 提供本地资源（Capacitor 默认）；
 * - allowMixedContent：允许连接 http:// 与 ws:// 后端（本地调试用回环地址必需，
 *   生产建议 https/wss）。
 *
 * SystemBars（内置插件，面向 Android 15+ 强制 edge-to-edge 的新设备，如红米 K80）：
 * - insetsHandling 默认 'css'：WebView ≥140 时把系统栏 insets 透传给
 *   env(safe-area-inset-*)，内容沉浸延伸至挖孔屏/手势条后面（由页面 CSS 避让）；
 *   旧版 WebView 自动退回原生 padding，同样不会重叠；
 * - initialViewportFitValueHint：提前告知 index.html 带 viewport-fit=cover，
 *   避免插件探测 meta 期间发生一次布局跳动；
 * - style 'LIGHT'：应用 UI 恒为浅色，强制状态栏/手势条用深色图标
 *  （默认跟随系统夜间模式会在浅色背景上渲染白色图标，看不清）。
 */
const config: CapacitorConfig = {
  appId: 'com.jesse467.im',
  appName: '微语',
  webDir: 'out/renderer',
  android: {
    allowMixedContent: true
  },
  plugins: {
    SystemBars: {
      initialViewportFitValueHint: 'cover',
      style: 'LIGHT'
    }
  }
}

export default config
