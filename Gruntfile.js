const path = require('path');
const { execFile } = require('child_process');

const DIST = 'internal/server/gui/dist';

module.exports = function (grunt) {
  grunt.initConfig({
    clean: {
      dist: [DIST]
    },

    copy: {
      uifont: {
        files: [{
          expand: true,
          cwd: 'node_modules/@fontsource-variable/roboto/files/',
          src: ['roboto-latin-wght-normal.woff2'],
          dest: `${DIST}/fonts/`
        }]
      },
      img: {
        files: [{
          expand: true,
          cwd: 'source/img/',
          src: ['**/*'],
          dest: `${DIST}/img/`
        }]
      }
    },

    concat: {
      options: {
        separator: ';\n'
      },
      dist: {
        src: [
          'node_modules/chart.js/dist/chart.umd.js',
          'source/js/*.js'
        ],
        dest: `${DIST}/js/app.js`
      }
    },

    terser: {
      options: {
        compress: true,
        mangle: true,
        format: { comments: false }
      },
      dist: {
        files: {
          [`${DIST}/js/app.min.js`]: [`${DIST}/js/app.js`]
        }
      }
    },

    watch: {
      css: {
        files: ['source/scss/**/*.scss', 'internal/server/gui/templates/**/*.html'],
        tasks: ['tailwind']
      },
      js: {
        files: ['source/js/**/*.js'],
        tasks: ['concat', 'terser']
      },
      img: {
        files: ['source/img/**/*'],
        tasks: ['copy:img']
      }
    }
  });

  grunt.loadNpmTasks('grunt-contrib-clean');
  grunt.loadNpmTasks('grunt-contrib-copy');
  grunt.loadNpmTasks('grunt-contrib-concat');
  grunt.loadNpmTasks('grunt-terser');
  grunt.loadNpmTasks('grunt-contrib-watch');

  grunt.registerTask('tailwind', 'Compile Tailwind CSS via the Tailwind CLI', function () {
    const done = this.async();
    const bin = path.join('node_modules', '.bin', process.platform === 'win32' ? 'tailwindcss.cmd' : 'tailwindcss');
    execFile(bin, ['-i', 'source/css/app.css', '-o', `${DIST}/css/app.min.css`, '--minify'], { shell: true }, (err, stdout, stderr) => {
      if (stderr) grunt.log.writeln(stderr);
      if (err) {
        grunt.log.error(err);
        return done(false);
      }
      grunt.log.writeln(`File ${DIST}/css/app.min.css created.`);
      done();
    });
  });

  grunt.registerTask('icons', 'Build SVG sprite from heroicons', function () {
    const fs = require('fs');
    const icons = [
      'arrow-down-circle', 'arrow-path', 'arrow-right-on-rectangle', 'arrow-up-circle',
      'bars-3', 'bolt', 'chart-bar', 'check-circle', 'chevron-left', 'chevron-right',
      'code-bracket', 'document-text', 'exclamation-triangle', 'funnel',
      'inbox', 'information-circle', 'key', 'link', 'lock-closed',
      'no-symbol', 'pencil', 'plus', 'plus-circle', 'server',
      'trash', 'user', 'x-circle', 'x-mark'
    ];

    const baseDir = path.join('node_modules', 'heroicons', '24', 'solid');
    let symbols = '';

    for (const name of icons) {
      const svgPath = path.join(baseDir, name + '.svg');
      const content = fs.readFileSync(svgPath, 'utf8');
      const viewBoxMatch = content.match(/viewBox="([^"]+)"/);
      const viewBox = viewBoxMatch ? viewBoxMatch[1] : '0 0 24 24';
      const inner = content
        .replace(/<svg[^>]*>/, '')
        .replace(/<\/svg>/, '')
        .trim();
      symbols += `  <symbol id="${name}" viewBox="${viewBox}" fill="currentColor">${inner}</symbol>\n`;
    }

    const sprite = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" style="display:none">\n${symbols}</svg>\n`;

    const outDir = 'source/img';
    if (!fs.existsSync(outDir)) fs.mkdirSync(outDir, { recursive: true });
    fs.writeFileSync(path.join(outDir, 'icons.svg'), sprite);
    grunt.log.writeln('SVG sprite built with ' + icons.length + ' icons → source/img/icons.svg');
  });

  grunt.registerTask('build', ['clean', 'icons', 'copy', 'tailwind', 'concat', 'terser']);
  grunt.registerTask('default', ['build']);
};
