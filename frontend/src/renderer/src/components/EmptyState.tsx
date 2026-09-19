import type { ReactNode } from 'react'
import { motion } from 'framer-motion'

/** 空态占位：幽灵图标 + 主文案 + 副文案，入场带轻微浮动动画 */
export function EmptyState({
  icon,
  title,
  hint,
  children
}: {
  icon: ReactNode
  title: string
  hint?: string
  children?: ReactNode
}): JSX.Element {
  return (
    <motion.div
      className="empty-state"
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25 }}
    >
      <motion.div
        className="empty-state-icon"
        animate={{ y: [0, -6, 0] }}
        transition={{ duration: 3.2, repeat: Infinity, ease: 'easeInOut' }}
      >
        {icon}
      </motion.div>
      <div className="empty-state-title">{title}</div>
      {hint && <div className="empty-state-hint">{hint}</div>}
      {children}
    </motion.div>
  )
}
