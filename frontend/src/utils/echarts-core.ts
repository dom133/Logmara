// Registers only the echarts pieces Dashboard.tsx actually uses (line +
// pie charts). Importing the full 'echarts' package instead of this pulls
// in every chart type and component, adding well over a MB to the bundle
// for features nothing in this app renders.
import * as echarts from 'echarts/core'
import { LineChart, PieChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent, TitleComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([LineChart, PieChart, GridComponent, TooltipComponent, LegendComponent, TitleComponent, CanvasRenderer])

export default echarts
