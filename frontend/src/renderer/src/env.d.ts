/// <reference types="vite/client" />

/**
 * 渲染层可用的构建期环境变量（Vite 约定 VITE_ 前缀）。
 *
 * 用途：决定服务器地址的「默认值」，本地调试不配置时回退本地回环地址；
 * 部署到公网（如阿里云）时，构建前注入实际地址，例如：
 *
 *   VITE_ACCOUNT_BASE=https://account.example.com \
 *   VITE_CHAT_BASE=https://chat.example.com \
 *   VITE_WS_BASE=wss://chat.example.com/ws pnpm build
 *
 * 也可写入 frontend/.env.production（该文件不入库），或临时用 UI 设置页覆盖。
 * 注意：UI 设置页保存的地址优先级高于环境变量，恢复默认即可回到环境变量值。
 */
interface ImportMetaEnv {
  readonly VITE_ACCOUNT_BASE?: string
  readonly VITE_CHAT_BASE?: string
  readonly VITE_WS_BASE?: string
}
