import QtQuick 2.5
import QtQuick.Controls 1.1
import QtQuick.Layouts 1.0
import QtQuick.Window 2.2

Window {
  id: topwindow

  visible: true
  title: "TermEmu-QML"
  width: 600
  height: 500

  function initialize() {
    ctrl.setView(terminal)
  }

  Terminal {
    id: terminal
    anchors.fill: parent
  }

}
