import { useSyncExternalStore } from "react"
import { Navigate, Route, Routes } from "react-router"

import { Shell } from "@/components/shell"
import { token } from "@/lib/api"
import { CertificatePage } from "@/pages/certificate"
import { DevicesPage } from "@/pages/devices"
import { LoginPage } from "@/pages/login"
import { ModulesPage } from "@/pages/modules"
import { ModuleEditorPage } from "@/pages/modules/editor"
import { RecordsPage } from "@/pages/records"
import { SystemPage } from "@/pages/system"

export function App() {
  const bearer = useSyncExternalStore(token.subscribe, token.get)
  if (!bearer) return <LoginPage />
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<RecordsPage />} />
        <Route path="devices" element={<DevicesPage />} />
        <Route path="modules" element={<ModulesPage />} />
        <Route path="modules/new" element={<ModuleEditorPage />} />
        <Route path="modules/:id" element={<ModuleEditorPage />} />
        <Route path="certificate" element={<CertificatePage />} />
        <Route path="system" element={<SystemPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}
