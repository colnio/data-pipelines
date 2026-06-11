import { Badge } from '@mantine/core'

const STATE_COLORS: Record<string, string> = {
  awaiting_review: 'yellow',
  approved: 'green',
  changes_requested: 'orange',
  quarantined: 'red',
  published: 'blue',
  declared: 'gray',
  processing: 'cyan',
}

interface StateBadgeProps {
  state: string
}

export function StateBadge({ state }: StateBadgeProps) {
  const color = STATE_COLORS[state] ?? 'gray'
  return (
    <Badge color={color} variant="light" size="sm">
      {state.replace(/_/g, ' ')}
    </Badge>
  )
}
