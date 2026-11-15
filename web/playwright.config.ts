import { defineConfig } from '@playwright/test'
export default defineConfig({testDir:'e2e',use:{baseURL:'http://localhost:4175',channel:process.env.CI?undefined:'chrome'},webServer:{command:'npx vite --port 4175 --strictPort',url:'http://localhost:4175'}})
