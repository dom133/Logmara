import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useCrud } from './useCRUD'
import { FormInstance, message } from 'antd'

vi.mock('antd', async () => {
  const antd = await vi.importActual<typeof import('antd')>('antd')
  return {
    ...antd,
    message: {
      success: vi.fn(),
      error: vi.fn(),
    },
  }
})

describe('useCRUD', () => {
  const mockForm = {
    resetFields: vi.fn(),
    setFieldsValue: vi.fn(),
  } as unknown as FormInstance

  const loadData = vi.fn()
  const createItem = vi.fn()
  const updateItem = vi.fn()
  const deleteItem = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    loadData.mockResolvedValue([{ id: 1, name: 'item1' }])
    createItem.mockResolvedValue({})
    updateItem.mockResolvedValue({})
    deleteItem.mockResolvedValue({})
  })

  it('initializes with loading=true and empty items', async () => {
    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    expect(result.current.loading).toBe(true)
    expect(result.current.items).toEqual([])
    expect(result.current.modalOpen).toBe(false)
    expect(result.current.editing).toBeNull()

    // flush the mount-triggered refresh() so its state update isn't left
    // dangling outside act() once the test exits
    await act(async () => {})
  })

  it('loads data on mount', async () => {
    renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    await act(async () => {})

    expect(loadData).toHaveBeenCalledTimes(1)
  })

  it('openCreate opens modal and resets form', async () => {
    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    await act(async () => {
      result.current.openCreate()
    })

    expect(result.current.modalOpen).toBe(true)
    expect(result.current.editing).toBeNull()
    expect(mockForm.resetFields).toHaveBeenCalled()
  })

  it('openEdit opens modal with item', async () => {
    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    await act(async () => {})

    await act(async () => {
      result.current.openEdit({ id: 1, name: 'item1' })
    })

    expect(result.current.modalOpen).toBe(true)
    expect(result.current.editing).toEqual({ id: 1, name: 'item1' })
  })

  it('closeModal closes modal and resets form', async () => {
    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    await act(async () => {
      result.current.openCreate()
    })

    await act(async () => {
      result.current.closeModal()
    })

    expect(result.current.modalOpen).toBe(false)
    expect(result.current.editing).toBeNull()
    expect(mockForm.resetFields).toHaveBeenCalledTimes(2)
  })

  it('handleCreate creates item and refreshes', async () => {
    loadData.mockResolvedValue([{ id: 2, name: 'item2' }])

    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    await act(async () => {})

    await act(async () => {
      await result.current.handleCreate({ name: 'new' })
    })

    expect(createItem).toHaveBeenCalledWith({ name: 'new' })
    expect(message.success).toHaveBeenCalledWith('Item created')
    expect(result.current.modalOpen).toBe(false)
  })

  it('handleCreate shows error on failure', async () => {
    createItem.mockRejectedValue({ response: { data: { error: 'create failed' } } })

    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    await act(async () => {})

    await act(async () => {
      await result.current.handleCreate({ name: 'new' })
    })

    expect(message.error).toHaveBeenCalledWith('create failed')
  })

  it('handleDelete deletes item and refreshes', async () => {
    loadData.mockResolvedValue([])

    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    await act(async () => {})

    await act(async () => {
      await result.current.handleDelete(1)
    })

    expect(deleteItem).toHaveBeenCalledWith(1)
    expect(message.success).toHaveBeenCalledWith('Item deleted')
  })

  it('uses custom messages when provided', async () => {
    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Widget',
        form: mockForm,
        messages: {
          created: 'Widget saved!',
        },
      })
    )

    await act(async () => {})

    await act(async () => {
      await result.current.handleCreate({ name: 'new' })
    })

    expect(message.success).toHaveBeenCalledWith('Widget saved!')
  })

  it('does not refetch in a loop when caller passes a new loadData closure every render', async () => {
    // Reproduces the Dashboards.tsx / Parsers.tsx pattern: an inline
    // `loadData: async () => {...}` literal gets a new identity on every
    // render. refresh()/the mount effect must not chase that identity.
    const { result, rerender } = renderHook(
      ({ tick }) =>
        useCrud({
          loadData: async () => loadData(tick),
          createItem,
          updateItem,
          deleteItem,
          entityName: 'Item',
          form: mockForm,
        }),
      { initialProps: { tick: 0 } },
    )

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(loadData).toHaveBeenCalledTimes(1)

    // Simulate several parent re-renders (e.g. from unrelated state updates)
    // each supplying a brand-new loadData closure.
    rerender({ tick: 1 })
    rerender({ tick: 2 })
    rerender({ tick: 3 })

    await act(async () => {})

    expect(loadData).toHaveBeenCalledTimes(1)
  })

  it('setItems accepts function updater', async () => {
    const { result } = renderHook(() =>
      useCrud({
        loadData,
        createItem,
        updateItem,
        deleteItem,
        entityName: 'Item',
        form: mockForm,
      })
    )

    await act(async () => {})

    await act(async () => {
      result.current.setItems((prev: typeof result.current.items) => [...prev, { id: 2, name: 'item2' }])
    })

    expect(result.current.items).toHaveLength(2)
  })
})