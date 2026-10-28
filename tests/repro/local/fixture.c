#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <time.h>
int main(int argc,char **argv) {
  setbuf(stdout,NULL); setbuf(stderr,NULL);
  printf("REPRO_STARTED mode=%s\n",argc>1?argv[1]:"ok");
  if(argc>1 && !strcmp(argv[1],"fail")) {fprintf(stderr,"REPRO_STDERR\n");return 42;}
  if(argc>1 && !strcmp(argv[1],"sleep")) {for(int i=0;i<120;i++){printf("REPRO_PROGRESS %d\n",i);sleep(1);}return 0;}
  if(argc>1 && !strcmp(argv[1],"memory")) {for(int i=0;i<2048;i++){char*p=malloc(1048576);if(!p)return 2;memset(p,1,1048576);printf("REPRO_ALLOC_MB %d\n",i+1);usleep(10000);}return 0;}
  if(argc>1 && !strcmp(argv[1],"drain"))sleep(3);
  if(argc>1 && !strcmp(argv[1],"cpu")){time_t end=time(NULL)+12;volatile unsigned long x=1;while(time(NULL)<end){for(int i=0;i<10000;i++)x=x*1664525+1013904223;}}
  puts("REPRO_OK");return 0;
}
