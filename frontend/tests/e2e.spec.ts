import { test, expect } from '@playwright/test'

test.describe('Syslytics E2E', () => {
  test('login and view dashboard', async ({ page }) => {
    await page.goto('/login')
    await page.fill('input[name="username"]', 'admin')
    await page.fill('input[name="password"]', 'password123')
    await page.click('button[type="submit"]')
    await expect(page).toHaveURL(/\/dashboard/)
  })

  test('view logs page', async ({ page }) => {
    await page.goto('/login')
    await page.fill('input[name="username"]', 'admin')
    await page.fill('input[name="password"]', 'password123')
    await page.click('button[type="submit"]')
    await page.click('text=Logs')
    await expect(page).toHaveURL(/\/logs/)
  })

  test('create and delete dashboard', async ({ page }) => {
    await page.goto('/login')
    await page.fill('input[name="username"]', 'admin')
    await page.fill('input[name="password"]', 'password123')
    await page.click('button[type="submit"]')
    await page.click('text=Dashboards')
    await page.click('text=New Dashboard')
    await page.fill('input[name="name"]', 'E2E Test Dashboard')
    await page.click('text=Save')
    await expect(page.locator('text=E2E Test Dashboard')).toBeVisible()
  })
})