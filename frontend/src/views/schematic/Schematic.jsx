// Top-level layout for the Schematic tab — the milestone-4 deliverable.
// Three columns: palette, canvas, inspector, with a status bar on the bottom.
// The exact column proportions come from mockups/01_schematic_editor.html.

import Canvas from './Canvas.jsx';
import Inspector from './Inspector.jsx';
import Palette from './Palette.jsx';
import StatusBar from './StatusBar.jsx';

export default function Schematic() {
  return (
    <div className="schematic">
      <div className="schematic-workspace">
        <Palette />
        <Canvas />
        <Inspector />
      </div>
      <StatusBar />
    </div>
  );
}
