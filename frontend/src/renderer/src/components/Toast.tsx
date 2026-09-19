import { AnimatePresence, motion } from 'framer-motion'
import { useToastStore } from '@/store/toast'
import { CheckIcon, AlertIcon, BellIcon } from './icons'

/** 全局轻提示：顶部居中滑入，自动消失 */
export function ToastHost(): JSX.Element {
  const toasts = useToastStore((s) => s.toasts)

  return (
    <div className="toast-host">
      <AnimatePresence>
        {toasts.map((t) => (
          <motion.div
            key={t.id}
            className={`toast toast-${t.type}`}
            initial={{ opacity: 0, y: -18, scale: 0.95 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -12, scale: 0.96 }}
            transition={{ type: 'spring', stiffness: 420, damping: 32 }}
          >
            <span className="toast-icon">
              {t.type === 'success' ? (
                <CheckIcon width={14} height={14} />
              ) : t.type === 'error' ? (
                <AlertIcon width={14} height={14} />
              ) : (
                <BellIcon width={14} height={14} />
              )}
            </span>
            {t.text}
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  )
}
