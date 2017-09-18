import QtQuick 2.5
import QtQuick.Controls 1.1
import QtQuick.Layouts 1.0
import QtQuick.Window 2.2


Item {
  id: termRoot
  focus: true

  property var regionsToRender: []

  function regionChanged(x, y, w, str, fg, bg) {
    var syms = getSymbols(str);
    if (w != str.length) {
      console.log("str.len != w: ", w, str, syms);
    }
    // console.log("region: ", w, str, syms);
    regionsToRender.push([x, y, w, syms, fg, bg]);
    canvas.requestPaint();
  }

  function cursorMoved(x, y) {
    cursorX = x;
    cursorY = y;
    // console.log("cursorMoved:", x, y)
  }

  function colorsChanged(fg, bg) {
    fgColor = fg;
    // console.log("colorsChanged:", fg, bg)
  }

  function redrawAll() {
    ctrl.redrawAll();
  }


  function getSymbols(string) {
  	var index = 0;
  	var length = string.length;
  	var output = [];
  	for (; index < length - 1; ++index) {
  		var charCode = string.charCodeAt(index);
  		if (charCode >= 0xD800 && charCode <= 0xDBFF) {
  			charCode = string.charCodeAt(index + 1);
  			if (charCode >= 0xDC00 && charCode <= 0xDFFF) {
  				output.push(string.slice(index, index + 2));
  				++index;
  				continue;
  			}
  		}
  		output.push(string.charAt(index));
  	}
  	output.push(string.charAt(index));
  	return output;
  }


  property var keyMap

  Component.onCompleted: {
    keyMap = {};
    keyMap[Qt.Key_Backspace] = "backspace";
    keyMap[Qt.Key_Delete] = "delete";
    keyMap[Qt.Key_Left] = "left";
    keyMap[Qt.Key_Right] = "right";
    keyMap[Qt.Key_Up] = "up";
    keyMap[Qt.Key_Down] = "down";
    keyMap[Qt.Key_Home] = "home";
    keyMap[Qt.Key_End] = "end";
    keyMap[Qt.Key_PageUp] = "pgup";
    keyMap[Qt.Key_PageDown] = "pgdown";
    keyMap[Qt.Key_F1] = "F1";
    keyMap[Qt.Key_F2] = "F2";
    keyMap[Qt.Key_F3] = "F3";
    keyMap[Qt.Key_F4] = "F4";
    keyMap[Qt.Key_F5] = "F5";
    keyMap[Qt.Key_F6] = "F6";
    keyMap[Qt.Key_F7] = "F7";
    keyMap[Qt.Key_F8] = "F8";
    keyMap[Qt.Key_F9] = "F9";
    keyMap[Qt.Key_F10] = "F10";
    keyMap[Qt.Key_F11] = "F11";
    keyMap[Qt.Key_F12] = "F12";
  }

  Keys.onPressed: {
    if ((event.modifiers & (~Qt.KeypadModifier)) != 0) {
      console.log("key:", event.modifiers, event.key, event.text);
    }
    if (event.key == Qt.Key_F5) {
      redrawAll();
      return;
    }

    if (event.modifiers & Qt.KeypadModifier && event.text != "") {
      ctrl.specialKeyPressed("numpad"+event.text);
      return;
    }

    var special = keyMap[event.key];
    if (special != null) {
      ctrl.specialKeyPressed(special);
      return;
    }

    if (event.text != "") {
      ctrl.keyPressed(event.text);
      return;
    }

    console.log("Key? ", "0x"+event.key.toString(16));
  }

  anchors.fill: parent
  MouseArea {
    id: ma
    anchors.fill: parent
  }


  FontMetrics {
    id: fontMetrics
    font.family: "Hack"
    font.pointSize: 11

    property int cw: fontMetrics.boundingRect("x").width //fontMetrics.maximumCharacterWidth
    property int ch: fontMetrics.boundingRect("x").height //fontMetrics.lineSpacing
  }

  property int cursorX: 0
  property int cursorY: 0
  property int fgColor: 0
  // onFgColorChanged: {
  //   var ctx = {};
  //   canvas.setColor(ctx, fgColor);
  //   cursor.color = ctx.fillStyle;
  // }
  Rectangle {
    id: cursor
    x: cursorX * fontMetrics.cw
    y: cursorY * fontMetrics.ch
    width: fontMetrics.cw
    height: fontMetrics.ch
    color: "#ffffff"

    z: 100
  }

  Canvas {
    id: canvas
    anchors.fill: parent

    property string baseFont: fontMetrics.font.pixelSize + "px " + fontMetrics.font.family
    readonly property var colors16: ["#000000", "#dd0000", "#00dd00", "#dddd00", "#0000dd", "#dd00dd", "#00dddd", "#dddddd",
                                     "#666666", "#ff6666", "#66ff66", "#ffff66", "#6666ff", "#ff66ff", "#66ffff", "#ffffff"]
    readonly property var colors6: [0, 84, 135, 175, 215, 255]
    readonly property int modeMask:       0x3f << 24
    readonly property int modeBold:       1 << (0 + 24)
  	readonly property int modeDim:        1 << (1 + 24)
  	readonly property int modeItalic:     1 << (2 + 24)
  	readonly property int modeUnderline:  1 << (3 + 24)
  	readonly property int modeBlink:      1 << (4 + 24) // or
  	readonly property int modeReverse:    1 << (5 + 24)
  	readonly property int modeInvisible:  1 << (6 + 24)


    // var symbols = getSymbols('💩');
    // symbols.forEach(function(symbol) {
    // 	assert(symbol == '💩');
    // });

    function setColor(ctx, c) {
      var colorType = (c >> 31) & 1;
      if (colorType == 0) { // 256 color
        var col = c & 0xff;
        if (col < 16) { // 16 palette colors
          ctx.fillStyle = colors16[col];
          // console.log("pallette ", col, "->", ctx.fillStyle);
        } else if (col < 232) { // 216 6x6x6 cube
          col -= 16;
          var cs = [(col / 36) % 6, (col / 6) % 6, col % 6];
          var lighten = 2;
          for (var i = 0; i < cs.length; i++) {
            // if (cs[i] == 1) {
            //   cs[i] = cs[i] * (255 / 6) | 0;
            // } else if (cs[i] > 1) {
            //   cs[i] = (cs[i] + lighten) * (255 / (6 + lighten)) | 0;
            // }
            cs[i] = colors6[cs[i]|0];
            cs[i] = (cs[i] < 16? '0' : '') + cs[i].toString(16);
          }
          ctx.fillStyle = '#' + cs[0] + cs[1] + cs[2];
          // console.log("color ", col, r, g, b, "->", ctx.fillStyle);
        } else { // 24 grayscale
          col -= 232;
          col = col / 24.0 * 255;
          col |= 0;
          var s = (col < 16? '0' : '') + col.toString(16);
          ctx.fillStyle = '#' + s + s + s;
          // console.log("gray ", c & 0xff, col, s, "->", ctx.fillStyle);
        }
      } else {
        console.log("colorType fail:", colorType);
      }
      // [normal|italic|oblique] [normal|small-caps] [normal|bold|0..99] Npx|Npt family
      var style = "normal", variant = "normal", weight = "normal";
      if (c & modeBold) {
        weight = "bold";
      }
      if (c & modeItalic) {
        style = "italic";
      }
      ctx.font = style + " " + variant + " " + weight + " " + baseFont;
    }

    property var lastWidth: 0
    property var lastHeight: 0

    onPaint: {
      var regions = regionsToRender;
      regionsToRender = [];

      if (width != lastWidth || height != lastHeight) {
        lastWidth = width;
        lastHeight = height;
        redrawAll();
      }

      // console.log(fontMetrics.advanceWidth("@"));
      // console.log(fontMetrics.boundingRect("@").width);
      // console.log(fontMetrics.advanceWidth("@@"));
      // console.log(fontMetrics.boundingRect("@@").width);


      var cw = fontMetrics.cw;
      var ch = fontMetrics.ch;
      var ascent = fontMetrics.ascent;
      var underlinePos = fontMetrics.underlinePosition;
      var lineWidth = fontMetrics.lineWidth;
      // console.log("char: ", cw, ch);

      var ctx = getContext("2d");
      ctx.font = baseFont;
      // ctx.textBaseline = "top";

      var lastFG;

      for (var i = 0; i < regions.length; i++) {
        var r = regions[i];
        var x = r[0];
        var y = r[1];
        var w = r[2];
        var syms = r[3];
        var fg = r[4];
        var bg = r[5];

        // console.log("region:", x, y, w, str, fg, bg);

        if (fg & modeReverse) {
          // console.log("reverse: ", str);
          var temp = fg;
          fg = (fg & modeMask) | bg;
          bg = temp;
        }

        // console.log("bg: ", bg, bg.toString(16), "  fg: ", fg, fg.toString(16), "  ", syms);

        setColor(ctx, bg);
        // ctx.fillStyle = "#000000";
        ctx.fillRect(x*cw, y*ch, w*cw, ch);

        setColor(ctx, fg);
        // ctx.fillStyle = "#ffffff";
        for (var xi = 0; xi < w; xi++) {
          ctx.fillText(syms[xi], (x+xi)*cw, y*ch + ascent);
        }

        if (fg & modeUnderline) {
          ctx.fillRect(x*cw, y*ch + ascent + underlinePos, w*cw, lineWidth)
        }
      }
    }
  }
}
