const path = require('path');
const { execFile } = require('child_process');

const DIST = 'internal/server/gui/dist';

module.exports = function (grunt) {
  grunt.initConfig({
    clean: {
      dist: [DIST]
    },

    copy: {
      fonts: {
        files: [{
          expand: true,
          cwd: 'node_modules/bootstrap-icons/font/fonts/',
          src: ['*.woff', '*.woff2'],
          dest: `${DIST}/fonts/`
        }]
      },
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
        files: ['source/css/**/*.css', 'internal/server/gui/templates/**/*.html'],
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

  grunt.registerTask('build', ['clean', 'copy', 'tailwind', 'concat', 'terser']);
  grunt.registerTask('default', ['build']);
};
