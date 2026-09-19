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
 */
const config: CapacitorConfig = {
  appId: 'com.jesse467.im',
  appName: '微语',
  webDir: 'out/renderer',
  android: {
    allowMixedContent: true
  }
}

export default config
