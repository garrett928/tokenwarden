import { useEffect, useState } from 'react'
import Layout from './components/Layout'
import { parseRoute, type Route } from './router'
import Dashboard from './pages/Dashboard'
import JobsList from './pages/JobsList'
import CreateJob from './pages/CreateJob'
import JobDetail from './pages/JobDetail'
import SchedulerConfig from './pages/SchedulerConfig'

function routeToPage(route: Route) {
  switch (route.name) {
    case 'home':
      return <CreateJob />
    case 'dashboard':
      return <Dashboard />
    case 'jobs':
      return <JobsList />
    case 'job-detail':
      return <JobDetail id={route.id} />
    case 'scheduler':
      return <SchedulerConfig />
  }
}

export default function App() {
  const [route, setRoute] = useState<Route>(parseRoute(window.location.hash))

  useEffect(() => {
    const onHashChange = () => setRoute(parseRoute(window.location.hash))
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  return <Layout>{routeToPage(route)}</Layout>
}
