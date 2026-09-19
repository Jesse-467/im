import { useEffect } from 'react'
import { AnimatePresence } from 'framer-motion'
import { useAuthStore } from '@/store/auth'
import { AuthView } from '@/views/AuthView'
import { Shell } from '@/views/Shell'
import { ToastHost } from '@/components/Toast'

/**
 * 应用根节点：登录态二选一切换（AuthView / Shell），
 * 切换通过 AnimatePresence 产生「卡片退场 → 主框架入场」的过渡。
 */
export default function App(): JSX.Element {
  const status = useAuthStore((s) => s.status)
  const bootstrap = useAuthStore((s) => s.bootstrap)

  useEffect(() => {
    void bootstrap()
  }, [bootstrap])

  return (
    <>
      <AnimatePresence mode="wait">
        {status === 'authed' ? (
          <Shell key="shell" />
        ) : status === 'guest' ? (
          <AuthView key="auth" />
        ) : (
          <div key="boot" className="boot-screen">
            <span className="boot-pulse" />
          </div>
        )}
      </AnimatePresence>
      <ToastHost />
    </>
  )
}
