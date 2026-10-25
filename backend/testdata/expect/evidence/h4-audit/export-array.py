from pathlib import Path
import subprocess
s=Path('internal/store/inflow.go').read_text()
def fn(name):
 start=s.index('func '+name+'(');end=s.index('\n}',start)+2
 return s[start:end]
start=s.index('var inflowColumns = ');end=s.index('\n\n',start)
code='package main\nimport("fmt";"strings";"os")\n'+s[start:end]+'\n'+fn('inflowBucketSQL')+'\n'+fn('inflowPublishSelectSQL')+'''
func main(){
 source,e:=os.ReadFile("testdata/expect/evidence/h4-audit/sparse.sql");if e!=nil{panic(e)}
 if e=os.WriteFile("testdata/expect/evidence/h4-audit/array.sql",[]byte(inflowPublishSelectSQL(string(source),true)),0600);e!=nil{panic(e)}
 cols:=make([]string,40);cols[0]="$1::bigint";for i:=1;i<40;i++{cols[i]=fmt.Sprintf("c%d",i)}
 local:="SELECT "+strings.Join(cols,",")+" FROM h4_actual_input"
 if e=os.WriteFile("testdata/expect/evidence/h4-audit/local-array.sql",[]byte(inflowPublishSelectSQL(local,true)),0600);e!=nil{panic(e)}
 fmt.Println("exported SAME production array-expression helper for readonly cloud and local staging publication")
}
'''
Path('/tmp/h4-array-export.go').write_text(code)
subprocess.run(['go','run','/tmp/h4-array-export.go'],check=True)
