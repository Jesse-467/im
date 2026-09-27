import { useEffect } from 'react'
import { useAuthStore } from '@/store/auth'
import { AuthView } from '@/views/AuthView'
import { Shell } from '@/views/Shell'
import { ToastHost } from '@/components/Toast'

/**
 * 应用根节点：登录态二选一切换（AuthView / Shell），
 * 登录态切换立即替换视图，入场动画由各视图负责，避免嵌套退场阻塞页面。
 */
export default function App(): JSX.Element {
  const status = useAuthStore((s) => s.status)
  const bootstrap = useAuthStore((s) => s.bootstrap)

  useEffect(() => {
    void bootstrap()
  }, [bootstrap])

  return (
    <>
      {status === 'authed' ? (
        <Shell key="shell" />
      ) : status === 'guest' ? (
        <AuthView key="auth" />
      ) : (
        <div key="boot" className="boot-screen">
          <span className="boot-pulse" />
        </div>
      )}
      <ToastHost />
    </>
  )
}
