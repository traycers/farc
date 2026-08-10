import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import StoragesIndexPage from './pages/storages/StoragesIndexPage'
import StorageNewPage from './pages/storages/StorageNewPage'
import StorageEditPage from './pages/storages/StorageEditPage'
import ChannelsIndexPage from './pages/channels/ChannelsIndexPage'
import ChannelNewPage from './pages/channels/ChannelNewPage'
import ChannelEditPage from './pages/channels/ChannelEditPage'
import PlayerPage from './pages/PlayerPage'
import JournalPage from './pages/JournalPage'
import FblockLivePage from './pages/FblockLivePage'
import FblockListPage from './pages/FblockListPage'
import FblockStatusPage from './pages/FblockStatusPage'

function navLinkClass({ isActive }: { isActive: boolean }) {
  return `nav-link${isActive ? ' active' : ''}`
}

export default function App() {
  return (
    <>
      <nav className="navbar navbar-expand navbar-dark bg-dark">
        <div className="container">
          <span className="navbar-brand mb-0 h1">farc admin</span>
          <div className="navbar-nav">
            <NavLink to="/storages" className={navLinkClass}>
              Storages
            </NavLink>
            <NavLink to="/channels" className={navLinkClass}>
              Channels
            </NavLink>
            <NavLink to="/player" className={navLinkClass}>
              Player
            </NavLink>
            <NavLink to="/journal" className={navLinkClass}>
              Журнал
            </NavLink>
            <NavLink to="/fblock-live" className={navLinkClass}>
              Fblock (live)
            </NavLink>
            <NavLink to="/fblocks" className={navLinkClass}>
              Fblocks
            </NavLink>
          </div>
        </div>
      </nav>
      <main>
        <Routes>
          <Route path="/" element={<Navigate to="/storages" replace />} />
          <Route path="/storages">
            <Route index element={<StoragesIndexPage />} />
            <Route path="new" element={<StorageNewPage />} />
            <Route path=":id/edit" element={<StorageEditPage />} />
          </Route>
          <Route path="/channels">
            <Route index element={<ChannelsIndexPage />} />
            <Route path="new" element={<ChannelNewPage />} />
            <Route path=":id/edit" element={<ChannelEditPage />} />
          </Route>
          <Route path="/player" element={<PlayerPage />} />
          <Route path="/journal" element={<JournalPage />} />
          <Route path="/fblock-live" element={<FblockLivePage />} />
          <Route path="/fblocks" element={<FblockListPage />} />
          <Route path="/fblock-status" element={<FblockStatusPage />} />
        </Routes>
      </main>
    </>
  )
}
