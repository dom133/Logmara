import { useState, useEffect, useCallback, useRef } from 'react';

export interface ColumnDef {
  key: string;
  width?: number;
  title?: React.ReactNode;
  [key: string]: unknown;
}

interface UseColumnWidthsReturn {
  enhanceColumns: (cols: ColumnDef[]) => ColumnDef[];
  hasChanges: boolean;
  reset: () => void;
}

const MIN_WIDTH = 60;

export function useColumnWidths(storageKey: string, defaultCols: ColumnDef[]): UseColumnWidthsReturn {
  const [widths, setWidths] = useState<Record<string, number>>({});
  const widthsRef = useRef(widths);
  widthsRef.current = widths;

  const defaultColsRef = useRef(defaultCols);
  defaultColsRef.current = defaultCols;

  const defaultsRef = useRef<Record<string, number>>({});
  {
    const d: Record<string, number> = {};
    for (const col of defaultColsRef.current) {
      if (col.width) d[col.key] = col.width;
    }
    defaultsRef.current = d;
  }

  useEffect(() => {
    try {
      const stored = localStorage.getItem(storageKey);
      if (stored) setWidths(JSON.parse(stored));
    } catch { /* ignore */ }
  }, [storageKey]);

  useEffect(() => {
    if (Object.keys(widthsRef.current).length > 0) {
      localStorage.setItem(storageKey, JSON.stringify(widthsRef.current));
    }
  }, [widths, storageKey]);

  const hasChanges = Object.keys(widthsRef.current).some((k) => widthsRef.current[k] !== defaultsRef.current[k]);

  const reset = useCallback(() => {
    setWidths({});
    localStorage.removeItem(storageKey);
  }, [storageKey]);

  const getEffectiveWidth = useCallback(
    (col: ColumnDef) => widthsRef.current[col.key] ?? col.width ?? defaultsRef.current[col.key],
    [],
  );

  const onMouseDown = useCallback(
    (col: ColumnDef, e: React.MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      const startX = e.clientX;
      const startWidth = getEffectiveWidth(col);

      const onMouseMove = (me: MouseEvent) => {
        const newWidth = Math.max(MIN_WIDTH, startWidth + (me.clientX - startX));
        setWidths((prev) => ({ ...prev, [col.key]: newWidth }));
      };

      const onMouseUp = () => {
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
      };

      document.addEventListener('mousemove', onMouseMove);
      document.addEventListener('mouseup', onMouseUp);
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    },
    [getEffectiveWidth],
  );

  const enhanceColumns = useCallback(
    (cols: ColumnDef[]) => {
      return cols.map((col) => ({
        ...col,
        width: getEffectiveWidth(col),
        onHeaderCell: () => ({ style: { position: 'relative' as const } }),
        title: (
          <div style={{ display: 'flex', alignItems: 'center', width: '100%' }}>
            <span style={{ flex: 1 }}>{col.title}</span>
            <div
              onMouseDown={(e) => onMouseDown(col, e)}
              style={{
                width: '4px',
                height: '100%',
                minHeight: '20px',
                cursor: 'col-resize',
                backgroundColor: 'transparent',
                transition: 'background-color 0.2s',
                flexShrink: 0,
              }}
              onMouseEnter={(e) => { (e.target as HTMLElement).style.backgroundColor = '#1890ff40'; }}
              onMouseLeave={(e) => { (e.target as HTMLElement).style.backgroundColor = 'transparent'; }}
            />
          </div>
        ),
      }));
    },
    [getEffectiveWidth, onMouseDown],
  );

  return { enhanceColumns, hasChanges, reset };
}