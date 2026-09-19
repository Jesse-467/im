import type { ReactNode } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { CloseIcon } from './icons'

interface ModalProps {
  open: boolean
  title: string
  width?: number
  onClose: () => void
  children: ReactNode
}

/**
 * 居中模态框：遮罩淡入 + 卡片弹性放大，退出时反向收场。
 * 承载「加好友 / 建群 / 服务器设置 / 退出群聊」等轻操作。
 */
export function Modal({ open, title, width = 420, onClose, children }: ModalProps): JSX.Element {
  return (
    <AnimatePresence>
      {open && (
        <motion.div
          className="modal-overlay"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.18 }}
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) onClose()
          }}
        >
          <motion.div
            className="modal-card"
            style={{ width }}
            initial={{ opacity: 0, scale: 0.92, y: 16 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.95, y: 10 }}
            transition={{ type: 'spring', stiffness: 380, damping: 30 }}
          >
            <header className="modal-header">
              <h3>{title}</h3>
              <button className="icon-btn" onClick={onClose} title="关闭">
                <CloseIcon width={16} height={16} />
              </button>
            </header>
            <div className="modal-body">{children}</div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  )
}
