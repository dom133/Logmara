import { Skeleton } from 'antd'

export default function SkeletonLoader({ rows = 3 }: { rows?: number }) {
  return <Skeleton active paragraph={{ rows }} />
}