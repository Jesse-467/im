import { avatarGradient, initialOf } from '@/core/format'

interface AvatarProps {
  name: string
  seed?: string
  url?: string
  size?: number
  online?: boolean
  square?: boolean
}

/**
 * QQ 风格圆角方形头像。
 * 有 URL 用图片，否则用名字首字 + 稳定散列渐变兜底。
 */
export function Avatar({
  name,
  seed,
  url,
  size = 40,
  online = false,
  square = true
}: AvatarProps): JSX.Element {
  const style: React.CSSProperties = {
    width: size,
    height: size,
    borderRadius: square ? Math.max(8, Math.round(size * 0.28)) : '50%',
    background: avatarGradient(`${seed ?? ''}${name}`)
  }

  return (
    <span className="avatar-wrap" style={{ width: size, height: size }}>
      {url ? (
        <img className="avatar-img" src={url} alt={name} style={style} draggable={false} />
      ) : (
        <span className="avatar-fallback" style={{ ...style, fontSize: size * 0.42 }}>
          {initialOf(name)}
        </span>
      )}
      {online && <span className="avatar-online-dot" />}
    </span>
  )
}
