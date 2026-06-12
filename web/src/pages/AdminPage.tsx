import { Box, Tabs, Title } from '@mantine/core'
import { UsersTab } from './admin/UsersTab'
import { AgentsTab } from './admin/AgentsTab'
import { JobsTab } from './admin/JobsTab'
import { NotificationsTab } from './admin/NotificationsTab'

export function AdminPage() {
  return (
    <Box>
      <Title order={3} mb="md">
        Admin
      </Title>
      <Tabs defaultValue="users" keepMounted={false}>
        <Tabs.List mb="md">
          <Tabs.Tab value="users">Users</Tabs.Tab>
          <Tabs.Tab value="agents">Agents</Tabs.Tab>
          <Tabs.Tab value="jobs">Jobs &amp; Audit</Tabs.Tab>
          <Tabs.Tab value="notifications">Notifications</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="users">
          <UsersTab />
        </Tabs.Panel>
        <Tabs.Panel value="agents">
          <AgentsTab />
        </Tabs.Panel>
        <Tabs.Panel value="jobs">
          <JobsTab />
        </Tabs.Panel>
        <Tabs.Panel value="notifications">
          <NotificationsTab />
        </Tabs.Panel>
      </Tabs>
    </Box>
  )
}
